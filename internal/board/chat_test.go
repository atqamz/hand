package board_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func reply(text string) string {
	return `{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestChatKeepsOrderWithoutNumbers(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("first reply"), reply("second reply"), reply("third reply")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	third, second, first := strings.Index(tl, "third reply"), strings.Index(tl, "second reply"), strings.Index(tl, "first reply")
	if first < 0 || second < 0 || third < 0 || !(third < second && second < first) {
		t.Fatalf("replies out of order (%d %d %d):\n%s", third, second, first, tl)
	}
	contains(t, "slug", tl, `<header class="slug"><span class="who">S1</span>`)
	lacks(t, "numbers", tl, "NO. ")
	older := get(t, fx.handler(), "/supervisor/log?before=4")
	contains(t, "paging", older, "second reply", "first reply")
	lacks(t, "paging", older, "third reply")
}

func TestYourNotesAreYours(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("please look")})
	if _, err := fx.st.AddSupervisorInput(context.Background(), "later please"); err != nil {
		t.Fatal(err)
	}
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "notes", tl, `<article class="card note" data-role="operator"><header class="slug"><span class="who">YOU</span>`, "please look", `<span class="label">Queued</span>`, "later please")
}

func TestHandLinesAreOneLine(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord(`[hand v1 wake]\ndecision.answered d1`)})
	contains(t, "hand line", region(get(t, fx.handler(), "/"), "timeline"), `<p class="hand-line" data-no="1"><span class="slug"><span class="who">HAND</span>`, `wake: decision.answered <a class="ref" href="/ref/d1">d1</a>`)
}

func TestTheWorkingLineNamesTheSupervisor(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "working", tl, "data-working", "s1 is working")
	lacks(t, "working", tl, "printhead")
}

func TestRefsLinkInDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("started t1 for you")})
	contains(t, "refs", region(get(t, fx.handler(), "/"), "timeline"), `<a class="ref" href="/ref/t1">t1</a>`)
}
