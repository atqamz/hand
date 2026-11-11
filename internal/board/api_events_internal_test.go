package board

import (
	"slices"
	"testing"
	"time"
)

func apiLine(role, at string) APIEntry { return APIEntry{Role: role, At: at, Text: role + at} }

func TestTheTimelineDropsOnlyWhatLeftTheRangeAndOnlyWhenSomethingChanged(t *testing.T) {
	s := &apiStream{entries: map[string]apiSent{}}
	page := APITimeline{Entries: []APIEntry{
		apiLine("supervisor", "2026-11-01T05:03:00Z"),
		apiLine("hand", "2026-11-01T05:02:00Z"),
		apiLine("operator", "2026-11-01T05:01:00Z"),
	}}
	if _, changed := s.timeline(page); !changed {
		t.Fatal("the first page is a change")
	}
	s.started = true
	to := s.to
	if _, changed := s.timeline(page); changed || s.to != to {
		t.Fatalf("an unchanged page: changed %v, to %d", changed, s.to)
	}
	next := APITimeline{Entries: []APIEntry{apiLine("supervisor", "2026-11-01T05:03:00Z"), apiLine("operator", "2026-11-01T05:01:00Z")}}
	ev, _ := s.timeline(next)
	if !slices.Equal(ev.Drop, []string{"hand@2026-11-01T05:02:00Z"}) {
		t.Fatalf("drop = %q", ev.Drop)
	}
	cut := APITimeline{Entries: []APIEntry{apiLine("supervisor", "2026-11-01T05:03:00Z"), apiLine("hand", "2026-11-01T05:02:00Z")}}
	ev, _ = s.timeline(cut)
	if len(ev.Drop) != 0 {
		t.Fatalf("an entry at the page's oldest time or older left the window, not the timeline: drop %q", ev.Drop)
	}
	ev, changed := s.timeline(APITimeline{})
	if !changed || len(ev.Drop) != 2 {
		t.Fatalf("an empty page drops every entry: %q", ev.Drop)
	}
}

func TestACountdownOfMoreThanThreeDigitsOfMinutesHasNoDenyAt(t *testing.T) {
	for deadline, want := range map[string]bool{"4:55": true, "999:59": true, "1000:00": false, "99999999999999999999:00": false} {
		items := apiNeeds(needsView{Waits: []waiting{{Kind: "blocked", Ref: "s1", Prompt: prompt{Deadline: deadline}}}}, time.Now()).Items
		if got := items[0].DenyAt != ""; got != want {
			t.Fatalf("%s: deny_at %q", deadline, items[0].DenyAt)
		}
	}
}

func TestTheTimelineDropsTheOldLastKeyWhenTheWindowCutsASameSecondGroup(t *testing.T) {
	s := &apiStream{entries: map[string]apiSent{}}
	at := "2026-11-01T05:01:00Z"
	line := func(text string) APIEntry { return APIEntry{Role: "hand", At: at, Text: text} }
	s.timeline(APITimeline{Entries: []APIEntry{line("c"), line("b"), line("a")}})
	s.started = true
	ev, _ := s.timeline(APITimeline{Entries: []APIEntry{line("c"), line("b")}})
	if !slices.Equal(ev.Drop, []string{"hand@" + at + "#3"}) {
		t.Fatalf("drop = %q", ev.Drop)
	}
}
