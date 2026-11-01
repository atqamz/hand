package board_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const askScreen = "────────────────────────\n Bash command\n\n   git push origin main\n   Push the branch\n\n Do you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again for git push commands in /w/t1\n  3. No, and tell Claude what to do differently (esc)"

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

func TestAnAskWorkersPermissionShowsTheCommandAndThreeButtons(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	askAttempt(t, fx, "Fix login")
	fx.screen = askScreen
	h := fx.handler()
	q := region(get(t, h, "/"), "queue")
	contains(t, "ask item", q, `data-kind="worker"`, `<span class="badge">ask</span>`, "<pre class=\"screen\"><code>git push origin main\nPush the branch</code></pre>", `action="/attempt/a1/keys" data-fetch`)
	lacks(t, "ask item", q, "Do you want to proceed?</code>", "tell Claude what to do differently")
	want := [][2]string{{"1", "Approve"}, {"2", "Approve always for this project"}, {"3", "Deny"}}
	if got := buttons(q); !slices.Equal(got, want) {
		t.Fatalf("buttons = %q, want %q", got, want)
	}
	digest := luvus.ScreenDigest(askScreen)
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
}

func TestAStaleAskScreenIsRefused(t *testing.T) {
	fx := newFixture(t)
	askAttempt(t, fx, "Fix login")
	fx.screen = askScreen
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
	fx.screen = askScreen
	at := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	fx.options.Now = func() time.Time { return at.Add(time.Minute) }
	lacks(t, "default worker", region(get(t, fx.handler(), "/"), "queue"), `data-kind="worker"`)
	fx2 := newFixture(t)
	fx2.supervisor(t, state.AttemptRunning, "gen-1")
	askAttempt(t, fx2, "Fix login")
	fx2.screen = askScreen
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
