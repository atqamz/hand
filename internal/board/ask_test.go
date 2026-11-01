package board_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "claude-2.1.316-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func askAttempt(t *testing.T, fx *fixture, title string) state.Attempt {
	t.Helper()
	ctx := context.Background()
	task := active(t, fx.st, title)
	a, err := fx.st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}, Approve: state.ApproveAsk}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if a, err = fx.st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "3", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.NoteAttempt(ctx, a.ID, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	fx.panes = map[string]string{"3": "blocked"}
	return a
}

var keyButton = regexp.MustCompile(`<button name="key" value="([^"]+)">([^<]+)</button>`)

func buttons(q string) [][2]string {
	var out [][2]string
	for _, m := range keyButton.FindAllStringSubmatch(q, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

func TestAnAskWorkersPermissionShowsTheCommandAndTheScreensOwnKeys(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	askAttempt(t, fx, "Fix login")
	fx.screen = capture(t, "npm")
	h := fx.handler()
	q := region(get(t, h, "/"), "queue")
	contains(t, "ask item", q, `data-kind="worker"`, `<span class="badge">ask</span>`, "<pre class=\"screen\"><code>npm install left-pad</code></pre>", `action="/attempt/a1/keys" data-fetch`)
	lacks(t, "ask item", q, "Do you want to proceed?</code>", "switch to auto mode")
	want := [][2]string{{"1", "Approve"}, {"2", "Approve always: npm install"}, {"4", "Deny"}}
	if got := buttons(q); !slices.Equal(got, want) {
		t.Fatalf("buttons = %q, want %q", got, want)
	}
	digest := luvus.ScreenDigest(capture(t, "npm"))
	for i, b := range want {
		res := post(h, "/attempt/a1/keys", url.Values{"revision": {"7"}, "screen": {digest}, "after": {"0"}, "key": {b[0]}})
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s = %d", b[1], res.StatusCode)
		}
		got := fx.called()
		if len(got) != i+1 || !slices.Equal(got[i], []string{"attempt", "keys", "--revision", "7", "--screen", digest, "--after", "0", "a1", b[0]}) {
			t.Fatalf("%s called %q", b[1], got)
		}
	}
	if !strings.Contains(q, `name="revision" value="7"`) || !strings.Contains(q, `name="screen" value="`+digest+`"`) {
		t.Fatalf("buttons lack the screen's revision and digest:\n%s", q)
	}
	if res := post(h, "/supervisor/keys", url.Values{"revision": {"7"}, "key": {"4"}}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("supervisor key 4 = %d", res.StatusCode)
	}
}

func TestAStaleAskScreenIsRefused(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = capture(t, "npm")
	fx.fail = fmt.Errorf("%w: the screen changed since revision 6; nothing was sent; read it again", state.ErrConflict)
	rec := request(fx.handler(), "POST", "/attempt/a1/keys", url.Values{"csrf": {token}, "revision": {"6"}, "key": {"1"}}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "The screen changed, try again") {
		t.Fatalf("stale press = %d:\n%s", rec.Code, rec.Body.String())
	}
	fx.fail = errors.New("boom")
	if rec := request(fx.handler(), "POST", "/attempt/a1/keys", url.Values{"csrf": {token}, "revision": {"7"}, "key": {"1"}}, true); rec.Code == http.StatusSeeOther {
		t.Fatalf("a failed press redirected")
	}
}

func TestAnAskPromptWithoutAlwaysOffersTwoButtons(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = "────────────────────────\n Edit file\n\n   main.go\n\n Do you want to make this edit?\n❯ 1. Yes\n  2. Yes, allow all edits during this session (shift+tab)\n  3. No, and tell Claude what to do differently (esc)"
	q := region(get(t, fx.handler(), "/"), "queue")
	if got, want := buttons(q), [][2]string{{"1", "Approve"}, {"3", "Deny"}}; !slices.Equal(got, want) {
		t.Fatalf("buttons = %q, want %q", got, want)
	}
}

func TestAnAskPromptForAFileOrAUrlShowsThePathAndTheQuestion(t *testing.T) {
	for name, tc := range map[string]struct {
		title string
		shown []string
		deny  string
	}{
		"edit":     {"Edit file: hello.txt (+5 lines)", []string{"hello.txt\n1  alpha\n2 -beta\n2 +BETA\n3  gamma\nDo you want to make this edit to hello.txt?"}, "3"},
		"write":    {"Create file: new.txt (+3 lines)", []string{"new.txt\n 1 line1\n 2 line2\nDo you want to create new.txt?"}, "3"},
		"webfetch": {"Fetch: Claude wants to fetch content from example.com (+3 lines)", []string{"url: https://example.com/a?b=1", "Do you want to allow Claude to fetch this content?"}, "3"},
		"long16":   {"Bash: echo line1 (+15 lines)", []string{"echo line1\n"}, "4"},
	} {
		fx := newFixture(t)
		askAttempt(t, fx, "Fix login")
		fx.screen = capture(t, name)
		q := region(get(t, fx.handler(), "/"), "queue")
		escaped := strings.NewReplacer("+", "&#43;")
		contains(t, name, q, escaped.Replace(tc.title))
		for _, shown := range tc.shown {
			contains(t, name, q, escaped.Replace(shown))
		}
		if got, want := buttons(q), [][2]string{{"1", "Approve"}, {tc.deny, "Deny"}}; !slices.Equal(got, want) {
			t.Fatalf("%s: buttons = %q, want %q", name, got, want)
		}
	}
}

func TestAnAskDialogWithoutAToolBodyFallsBackToTheRawScreen(t *testing.T) {
	for _, name := range []string{"mcp", "plan"} {
		fx := newFixture(t)
		askAttempt(t, fx, "Fix login")
		fx.screen = capture(t, name)
		q := region(get(t, fx.handler(), "/"), "queue")
		lacks(t, name, q, ">Approve</button>", ">Deny</button>")
		contains(t, name, q, `<span class="badge">ask</span>`, `<pre class="screen"><code>`)
	}
}

func TestAnAskScreenItDoesNotRecogniseFallsBackToTheRawScreen(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = "Pick a theme\n❯ 1. Dark\n  2. Light"
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "raw screen", q, `<span class="badge">ask</span>`, "<pre class=\"screen\"><code>Pick a theme", `>1 Dark</button>`)
	lacks(t, "raw screen", q, ">Approve</button>", ">Deny</button>")
}

func TestOnlyAnAskWorkerSkipsTheGraceWhileASupervisorRuns(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	fx.panes = map[string]string{"3": "blocked"}
	a := workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	fx.screen = capture(t, "npm")
	at := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	fx.options.Now = func() time.Time { return at.Add(time.Minute) }
	lacks(t, "default worker", region(get(t, fx.handler(), "/"), "queue"), `data-kind="worker"`)
	fx2 := newFixture(t)
	fx2.supervisor(t, state.AttemptRunning, "gen-1")
	askAttempt(t, fx2, "Fix login")
	fx2.screen = capture(t, "npm")
	fx2.options.Now = func() time.Time { return at.Add(time.Minute) }
	contains(t, "ask worker", region(get(t, fx2.handler(), "/"), "queue"), `data-kind="worker"`)
}

func TestTheAskBadgeShowsOnTheAskAttemptOnly(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	plain := workerAttempt(t, fx.st, active(t, fx.st, "Fix logout").ID)
	h := fx.handler()
	contains(t, "ask attempt page", get(t, h, "/attempt/a1"), `<span class="badge">ask</span>`)
	lacks(t, "plain attempt page", get(t, h, "/attempt/"+state.AttemptRef(plain.ID)), `<span class="badge">ask</span>`)
	contains(t, "ask task page", get(t, h, "/task/t1"), `<span class="badge">ask</span>`)
	lacks(t, "plain task page", get(t, h, "/task/t2"), `<span class="badge">ask</span>`)
}

func TestAnAskScreenWithoutItsHeaderShowsNoApprove(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = capture(t, "long16_last20")
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "cropped", q, `<span class="badge">ask</span>`, "<pre class=\"screen\"><code>")
	lacks(t, "cropped", q, ">Approve</button>", ">Deny</button>", "Approve always")
}

func TestALongAskCommandShowsAllItsLinesBesideApprove(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = capture(t, "long16")
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "long", q, "echo line1\necho line2", "echo line15\nrm -rf build</code>", ">Approve</button>")
	lacks(t, "long", q, "Approve always")
}

func TestTheSupervisorPickersLeaveAskProfilesOut(t *testing.T) {
	fx := newFixture(t)
	policy := `{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"xhigh"},"safe":{"harness":"claude","model":"sonnet","effort":"low","approve":"ask"}}}`
	if err := os.WriteFile(filepath.Join(fx.options.Home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	contains(t, "start", region(get(t, fx.handler(), "/"), "console"), `<option value="deep">`)
	lacks(t, "start", region(get(t, fx.handler(), "/"), "console"), `value="safe"`)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	lacks(t, "switch", region(get(t, fx.handler(), "/"), "console"), `value="safe"`)
}
