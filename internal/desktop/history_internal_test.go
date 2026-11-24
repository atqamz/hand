package desktop

import (
	"context"
	"errors"
	"strings"
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

type scriptedTimeline struct {
	ReadModel
	tl Timeline
}

func (m *scriptedTimeline) Timeline(ctx context.Context, fleet string, before int) (Timeline, error) {
	return m.tl, nil
}

func TestAPollMergesIntoAPageWithOlderEntriesAlreadyRead(t *testing.T) {
	m := &scriptedTimeline{tl: Timeline{Older: 40, Entries: []Entry{
		{Role: "supervisor", At: "2026-11-24T10:06:00Z", Text: "new"},
		{Role: "supervisor", At: "2026-11-24T10:05:00Z", Text: "newest before"},
	}}}
	a := &App{Model: m, Context: context.Background(), Fleet: Fleet{ID: "f0"}, Screen: HistoryScreen}
	a.hi.loaded = true
	a.hi.tl = Timeline{Older: 20, Entries: []Entry{
		{Role: "supervisor", At: "2026-11-24T10:05:00Z", Text: "newest before"},
		{Role: "supervisor", At: "2026-11-24T09:00:00Z", Text: "older"},
	}}
	a.pollHistory(true)
	if got := len(a.hi.tl.Entries); got != 3 {
		t.Fatalf("entries = %d (%+v), want the new one, the shared one and the older one read earlier", got, a.hi.tl.Entries)
	}
	if a.hi.tl.Older != 20 {
		t.Fatalf("older = %d, want the cursor of the pages already read (20)", a.hi.tl.Older)
	}
}

func TestHistoryFindChecksTheReplyTextBeforeAcceptingATimestamp(t *testing.T) {
	at := "2026-11-24T09:01:00Z"
	hit := HistoryHit{Ref: "y2", At: at}
	other := Entry{Role: "supervisor", At: at, Text: "A different reply."}
	want := Entry{Role: "supervisor", At: at, Text: "The reply that was asked for."}
	if _, ok := historyFind([]Entry{other}, hit, want.Text); ok {
		t.Fatal("a lone reply at the same time was taken without its text matching")
	}
	if e, ok := historyFind([]Entry{other}, hit, ""); !ok || e.Text != other.Text {
		t.Fatalf("a lone reply with no text to compare was refused: %+v %v", e, ok)
	}
	if e, ok := historyFind([]Entry{other, want}, hit, want.Text); !ok || e.Text != want.Text {
		t.Fatalf("picked %+v %v", e, ok)
	}
}

func TestHistoryFindPrefersTheExactTextOverASharedPrefix(t *testing.T) {
	at := "2026-11-24T09:01:00Z"
	hit := HistoryHit{Ref: "y2", At: at}
	long := strings.Repeat("same start ", 8)
	first := Entry{Role: "supervisor", At: at, Text: long + "one"}
	second := Entry{Role: "supervisor", At: at, Text: long + "two"}
	if e, ok := historyFind([]Entry{first, second}, hit, second.Text); !ok || e.Text != second.Text {
		t.Fatalf("picked %+v %v, want the second reply by its whole text", e, ok)
	}
	if _, ok := historyFind([]Entry{first, second}, hit, long+"three"); ok {
		t.Fatal("an ambiguous shared prefix was taken as a match")
	}
}

func TestAFailedTimelinePollOnTheHistoryPageMarksItStaleAndKeepsTheEntries(t *testing.T) {
	m := &flakyTimeline{}
	a := &App{Model: m, Context: context.Background(), Fleet: Fleet{ID: "f0"}, Screen: HistoryScreen, polling: true}
	a.hi.loaded = true
	a.hi.tl = Timeline{Entries: []Entry{{Role: "supervisor", At: "2026-11-24T09:00:00Z", Text: "held"}}}
	a.polled(a.pollGen, a.loadID, "f0", HistoryScreen, "", "", Poll{TimelineRead: true, TimelineErr: errors.New("the board did not answer in time")}, nil)
	if a.hi.stale == "" || len(a.hi.tl.Entries) != 1 {
		t.Fatalf("stale = %q entries = %d, want the marker over the held entries", a.hi.stale, len(a.hi.tl.Entries))
	}
	a.hi.skip = 0
	a.polled(a.pollGen, a.loadID, "f0", HistoryScreen, "", "", Poll{TimelineRead: true, TimelineFresh: true}, nil)
	if a.hi.stale != "" || a.hi.tl.Entries[0].Text != "back" {
		t.Fatalf("stale = %q entries = %+v after a later good read", a.hi.stale, a.hi.tl.Entries)
	}
}
