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

func TestTheWireNumbersDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("one"), reply("two"), reply("three")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	three, two, one := strings.Index(tl, `<span class="no">NO. 4</span>`), strings.Index(tl, `<span class="no">NO. 3</span>`), strings.Index(tl, `<span class="no">NO. 2</span>`)
	if one < 0 || two < 0 || three < 0 || !(three < two && two < one) {
		t.Fatalf("dispatch numbers out of order (%d %d %d):\n%s", three, two, one, tl)
	}
	contains(t, "slug", tl, `<header class="slug"><span class="who">S1</span><span class="no">NO. 4</span>`)
}

func TestYourNotesAreInPencil(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("please look")})
	if _, err := fx.st.AddSupervisorInput(context.Background(), "later please"); err != nil {
		t.Fatal(err)
	}
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "notes", tl, `<article class="dispatch note" data-role="operator"><header class="slug"><span class="who">YOU</span>`, "please look", `<span class="label">Queued</span>`, "later please")
}

func TestHandLinesAreServiceLines(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord(`[hand v1 wake]\ndecision.answered d1`)})
	contains(t, "service", region(get(t, fx.handler(), "/"), "timeline"), `<p class="service"><span class="slug"><span class="who">HAND</span>`, "wake: decision.answered d1")
}

func TestTheWorkingLineCarriesThePrintHead(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "working", tl, "data-working", "s1 is working", `<span class="printhead" aria-hidden="true"></span>`)
}

func TestRefsLinkInDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("started t1 for you")})
	contains(t, "refs", region(get(t, fx.handler(), "/"), "timeline"), `<a class="ref" href="/ref/t1">t1</a>`)
}
