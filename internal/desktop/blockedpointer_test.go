package desktop_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func blockedWithAnotherOpen(t *testing.T) *fixture {
	t.Helper()
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	worker := meetNeed("worker")
	worker.Ref, worker.Title, worker.Word, worker.Tone = "a2", "Worker a2 needs a key", "WORKER", "fail"
	st.Needs.Items = slices.Insert(slices.Clone(st.Needs.Items), 1, worker)
	st.Needs.Waiting = len(st.Needs.Items)
	f.status[fleetID] = st
	return f
}

func TestTheChatTabPointsAtTheBlockedAskAndOpensIt(t *testing.T) {
	const ask = "Do you want to proceed?"
	for _, width := range []int{860, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprint(width, " dark=", dark), func(t *testing.T) {
				tt := sized(t, newApp(t, blockedWithAnotherOpen(t)), width, 760, dark)
				if width > 900 {
					click(t, tt, "Worker a2 needs a key")
					if n := countExact(tt, ask); n != 0 {
						t.Fatalf("the blocked card still shows the ask %d times behind another open item", n)
					}
				}
				chatTab(t, tt, width)
				if n := countExact(tt, "The question is in Needs"); n != 2 {
					t.Fatalf("the pointer is shown %d times, want its label and its text once", n)
				}
				if width < 900 {
					if n := countExact(tt, ask); n != 0 {
						t.Fatalf("the Chat tab holds the ask %d times, want a pointer only", n)
					}
				}
				click(t, tt, "The question is in Needs")
				tt.Frame()
				if n := countExact(tt, ask); n != 1 {
					t.Fatalf("the ask is shown %d times after the pointer, want once", n)
				}
				mustText(t, tt, "1 Yes")
			})
		}
	}
}

func TestThePointerSaysScreenWhenThereIsNoAsk(t *testing.T) {
	tt := sized(t, newApp(t, blockedScreen(t, false)), 1100, 760, false)
	mustText(t, tt, "The screen is in Needs")
	mustNotText(t, tt, "The question is in Needs")
}

func TestNoPointerWithoutABlockedSupervisor(t *testing.T) {
	for _, c := range wordCases() {
		if c.word == "blocked" {
			continue
		}
		tt := sized(t, newApp(t, c.fixture(t)), 1100, 760, false)
		mustNotText(t, tt, "The question is in Needs")
		mustNotText(t, tt, "The screen is in Needs")
	}
}

func TestABlockedHeldSupervisorReadsBlocked(t *testing.T) {
	st := wordCases()[2].fixture(t).status[fleetID]
	sup := *st.Supervisor
	sup.Held = true
	st.Supervisor = &sup
	if got := desktop.SupervisorWord(st); got != "blocked" {
		t.Fatalf("a blocked and held supervisor reads %q, want blocked", got)
	}
}

func TestNoPointerWhileNeedsHoldsNoBlockedItem(t *testing.T) {
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	st.Needs.Items = slices.DeleteFunc(slices.Clone(st.Needs.Items), func(n desktop.Need) bool { return n.Kind == "blocked" })
	f.status[fleetID] = st
	tt := sized(t, newApp(t, f), 1100, 760, false)
	mustNotText(t, tt, "The question is in Needs")
	mustNotText(t, tt, "The screen is in Needs")
}

func TestAnUnreadStatusShowsNoPointer(t *testing.T) {
	f := blockedScreen(t, true)
	app := newApp(t, f)
	f.fail = errors.New("the status read failed")
	app.Refresh()
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "unknown")
	mustNotText(t, tt, "The question is in Needs")
}

func TestABlockedHeldSupervisorKeepsThePointer(t *testing.T) {
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Held = true
	st.Supervisor = &sup
	f.status[fleetID] = st
	tt := sized(t, newApp(t, f), 1100, 760, false)
	mustText(t, tt, "The question is in Needs")
}

func TestAnUnreadBlockedStatusLeavesTheComposerHintPlain(t *testing.T) {
	const hint = "s1 is waiting on a screen in Needs; your message waits too."
	f := blockedScreen(t, true)
	st := f.status[fleetID]
	st.ComposerHint = hint
	f.status[fleetID] = st
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	if n := countExact(tt, hint); n != 2 {
		t.Fatalf("a blocked supervisor's hint is shown %d times, want a link with its label and text", n)
	}
	f.fail = errors.New("the status read failed")
	app.Refresh()
	tt = sized(t, app, 1100, 760, false)
	if n := countExact(tt, hint); n > 1 {
		t.Fatalf("an unread status keeps the hint a link (%d)", n)
	}
}

func TestThePointerBringsTheBlockedItemIntoViewOnAWideWindow(t *testing.T) {
	const ask = "Do you want to proceed?"
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			app := newApp(t, blockedWithAnotherOpen(t))
			tt := sized(t, app, 1100, 560, dark)
			tt.Scroll(600, 300, 0, 4000)
			if y, max := desktop.BoardScroll(app); y == 0 || max == 0 {
				t.Fatalf("the work pane does not scroll: y=%v max=%v", y, max)
			}
			click(t, tt, "The question is in Needs")
			tt.Frame()
			tt.Frame()
			r, ok := tt.Find(ask)
			if !ok || r.H == 0 {
				t.Fatalf("the ask is not shown after the pointer: %q", brief(tt))
			}
			if r.Y < 0 || r.Y+r.H > 560 {
				t.Errorf("the ask is at y=%v h=%v, outside the 560 px window", r.Y, r.H)
			}
		})
	}
}

func TestThePointerScrollsTheBlockedItemIntoViewOnlyOnce(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			app := newApp(t, blockedWithAnotherOpen(t))
			tt := sized(t, app, 1100, 560, dark)
			tt.Scroll(600, 300, 0, 4000)
			_, max := desktop.BoardScroll(app)
			if max == 0 {
				t.Fatalf("the work pane does not scroll: max=%v", max)
			}
			click(t, tt, "The question is in Needs")
			tt.Frame()
			tt.Frame()
			if y, _ := desktop.BoardScroll(app); y >= max {
				t.Fatalf("the pointer left the work pane at y=%v of %v", y, max)
			}
			tt.Scroll(600, 300, 0, 4000)
			for range 5 {
				tt.Frame()
			}
			if y, _ := desktop.BoardScroll(app); y < max {
				t.Errorf("the work pane was pulled back to y=%v of %v after the reader scrolled away", y, max)
			}
		})
	}
}

func TestThePointerLinkIsDrawnInTheAccentColour(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			tt := sized(t, newApp(t, blockedWithAnotherOpen(t)), windowWidth, windowHeight, dark)
			link := bandRect(t, tt, "The question is in Needs")
			if n := countNear(tt.Image(), link, kit.ColorsFor(dark).Accent, 12); n < 40 {
				t.Errorf("%d accent pixels in the pointer link, want its text drawn in the accent", n)
			}
		})
	}
}
