package board_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func askBlock(body string) string {
	const open = `<div class="ask">`
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(body[start:], "</div></div>")
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end]
}

func TestTheSupervisorPageShowsTheScreenItsRevisionAndTheKeys(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Trust this folder?"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/supervisor")
	contains(t, "screen page", body,
		`data-region="sup"`,
		`<pre class="screen"><code>Trust this folder?`,
		`name="revision" value="7"`,
		`name="screen" value="`+luvus.ScreenDigest(fx.screen)+`"`,
		`action="/supervisor/keys"`,
		`data-ask`, `data-screen-keys`,
		`<button name="key" value="esc">Esc</button>`)
	lacks(t, "screen page", body, `data-region="queue"`, `id="composer"`)
}

func TestANetworkSupervisorPageKeepsTheScreenAndDropsTheForms(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/supervisor")
	contains(t, "read-only screen page", body, "<pre class=\"screen\"><code>Trust this folder?", `data-region="sup"`)
	lacks(t, "read-only screen page", body, `action="/supervisor/`, `data-ask`, `data-screen-keys`)
}

func TestTheSupervisorPageReportsAScreenItCannotRead(t *testing.T) {
	fx := newFixture(t)
	fx.readErr = errors.New("luvus did not answer")
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/supervisor")
	contains(t, "unread screen", body, "could not be read", "luvus did not answer")
	lacks(t, "unread screen", body, "<pre class=\"screen\">", `data-screen-keys`)
}

func TestTheSupervisorPageWithoutASupervisorIsAnEmptyState(t *testing.T) {
	body := get(t, newFixture(t).handler(), "/supervisor")
	contains(t, "empty screen page", body, "No supervisor is running.", `data-region="sup"`)
	lacks(t, "empty screen page", body, "<pre class=\"screen\">", `action="/supervisor/keys"`)
}

func TestAnOmpCardOffersOneSequencePerOption(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Pick one"
	fx.screen = "Which approach should I take?\n◉ Fast\n◯ Correct\nselect · n note · ↑/↓ move · esc cancel"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ask := askBlock(get(t, fx.handler(), "/supervisor"))
	if ask == "" {
		t.Fatal("the screen has no ask card")
	}
	contains(t, "omp ask", ask,
		"Which approach should I take?",
		`<button name="keys" value="enter">Fast</button>`,
		`<button name="keys" value="down enter">Correct</button>`)
	lacks(t, "omp ask", ask, `name="key"`, "select", "↑/↓")
}

func TestAnIdleScreenWithoutADialogOffersNoAskCard(t *testing.T) {
	for name, screen := range map[string]string{
		"claude bullet":         "● All done.",
		"claude bullet list":    "● Fixed login\n● Checked the tests",
		"numbered transcript":   "Here is the plan.\n1. reproduce\n2. fix\n3. test",
		"questioned transcript": "Which steps remain?\n1. reproduce\n2. fix\n3. test",
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t)
			fx.status, fx.hint = "idle", ""
			fx.screen = screen
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			body := get(t, fx.handler(), "/supervisor")
			contains(t, "idle screen", body, `<pre class="screen"><code>`+screen, `data-screen-keys`)
			lacks(t, "idle screen", body, `<div class="ask">`, `data-ask`, `name="keys" value=`)
		})
	}
}

func TestAnIdleDialogOffersItsOptions(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "idle", ""
	fx.screen = "Which approach should I take?\n◉ Fast\n◯ Correct\nselect · n note · ↑/↓ move · esc cancel"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ask := askBlock(get(t, fx.handler(), "/supervisor"))
	if ask == "" {
		t.Fatal("the dialog has no ask card")
	}
	contains(t, "idle dialog", ask, "Which approach should I take?", `<button name="keys" value="enter">Fast</button>`, `<button name="keys" value="down enter">Correct</button>`)
}

func TestANumberedCardOffersItsNumbers(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint = "blocked", "Pick one"
	fx.screen = "Which file should I change?\n1. main.go\n2. util.go\n3. test.go"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ask := askBlock(get(t, fx.handler(), "/supervisor"))
	if ask == "" {
		t.Fatal("the screen has no ask card")
	}
	contains(t, "numbered ask", ask,
		"Which file should I change?",
		`<button name="key" value="1">`,
		`<button name="key" value="2">`,
		`<button name="key" value="3">`)
	lacks(t, "numbered ask", ask, `name="keys"`)
}

func TestPressesTakeAnOrderedSequence(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	res := post(h, "/supervisor/keys", url.Values{"revision": {"7"}, "keys": {"down down enter"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sequence press = %d", res.StatusCode)
	}
	want := []string{"supervisor", "keys", "--revision", "7", "down", "down", "enter"}
	if got := fx.called(); len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("sequence press called %q, want %q", got, want)
	}
}

func TestASequenceWithADisallowedKeyIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	res := post(fx.handler(), "/supervisor/keys", url.Values{"revision": {"7"}, "keys": {"down ctrl+c"}})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("disallowed sequence = %d", res.StatusCode)
	}
	if got := fx.called(); len(got) != 0 {
		t.Fatalf("control called: %q", got)
	}
}

func TestAStaleSequenceIsRefused(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	fx.fail = fmt.Errorf("%w: the screen changed since revision 7; nothing was sent", state.ErrConflict)
	rec := request(fx.handler(), "POST", "/supervisor/keys", url.Values{"csrf": {token}, "revision": {"7"}, "keys": {"down enter"}}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "The screen changed, try again") {
		t.Fatalf("stale sequence = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestTheSupervisorPageCarriesTheLiveStatusStrip(t *testing.T) {
	fx := newFixture(t)
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	body := get(t, fx.handler(), "/supervisor")
	contains(t, "supervisor page", body,
		`data-page="supervisor"`,
		`data-region="status"`,
		`data-icon-working="/static/favicon-working.`,
		`<p id="link" class="link-state" role="status" hidden></p>`,
		`href="/supervisor"`,
		`data-waiting="1"`)
	lacks(t, "supervisor page", body, `class="skip"`, `data-region="queue"`)
}

func TestOnlyTheSupervisorStreamSendsTheScreen(t *testing.T) {
	fx := newFixture(t)
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	base := serve(t, quick(fx.st, fx.options))

	fleet, _ := stream(t, base+"/events?page=fleet")
	if names := eventNames(first(t, fleet, len(regionNames))); slices.Contains(names, "sup") {
		t.Fatalf("fleet events = %q", names)
	}

	screen, _ := stream(t, base+"/events?page=supervisor")
	if names := eventNames(first(t, screen, 2)); !slices.Equal(names, []string{"status", "sup"}) {
		t.Fatalf("supervisor events = %q", names)
	}
}

func TestTheScreenRegionFollowsTheRevision(t *testing.T) {
	fx := newFixture(t)
	fx.status = "blocked"
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	base := serve(t, quick(fx.st, fx.options))
	ch, _ := stream(t, base+"/events?page=supervisor")
	if names := eventNames(first(t, ch, 2)); !slices.Equal(names, []string{"status", "sup"}) {
		t.Fatalf("initial events = %q", names)
	}
	fx.mu.Lock()
	fx.revision, fx.screen = 8, "Which file?\n1. main.go\n2. util.go"
	fx.mu.Unlock()
	evs := until(t, ch, arrives("sup", "Which file?", `name="revision" value="8"`))
	if len(evs) == 0 {
		t.Fatal("the screen region did not change")
	}
}
