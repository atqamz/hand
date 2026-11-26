package desktop_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
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
