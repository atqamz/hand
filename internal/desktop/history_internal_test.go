package desktop

import (
	"context"
	"errors"
	"testing"
)

type flakyTimeline struct {
	ReadModel
	fail  bool
	reads int
}

func (m *flakyTimeline) Timeline(ctx context.Context, fleet string, before int) (Timeline, error) {
	m.reads++
	if m.fail {
		return Timeline{}, errors.New("the board did not answer in time")
	}
	return Timeline{Entries: []Entry{{Role: "supervisor", At: "2026-11-24T10:00:00Z", Text: "back"}}}, nil
}

func TestAFailingPollMarksTheLoadedConversationStaleAndRecovers(t *testing.T) {
	m := &flakyTimeline{fail: true}
	a := &App{Model: m, Context: context.Background(), Fleet: Fleet{ID: "f0"}, Screen: HistoryScreen}
	a.hi.loaded = true
	a.hi.tl = Timeline{Entries: []Entry{{Role: "supervisor", At: "2026-11-24T09:00:00Z", Text: "held"}}}
	a.pollHistory(true)
	if a.hi.stale == "" || a.hi.err != "" {
		t.Fatalf("stale = %q err = %q, want a stale marker over the held conversation", a.hi.stale, a.hi.err)
	}
	if len(a.hi.tl.Entries) != 1 || a.hi.tl.Entries[0].Text != "held" {
		t.Fatalf("the failing read dropped the held entries: %+v", a.hi.tl)
	}
	m.fail = false
	for range 20 {
		a.pollHistory(false)
	}
	if a.hi.stale != "" {
		t.Fatalf("stale = %q after the board answered", a.hi.stale)
	}
}

func TestSnippetSplitsTheMatchedWords(t *testing.T) {
	got := historySnippet("…a «kiln» and «amber» b")
	want := []snippetPart{{"…a ", false}, {"kiln", true}, {" and ", false}, {"amber", true}, {" b", false}}
	if len(got) != len(want) {
		t.Fatalf("parts = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if p := historySnippet("open « never closed"); len(p) != 1 || p[0].match {
		t.Fatalf("an unclosed mark is text: %+v", p)
	}
}
