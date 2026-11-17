package transcript

import "testing"

func op(text, at string) Entry  { return Entry{Role: "operator", Text: text, At: at} }
func sup(text, at string) Entry { return Entry{Role: "supervisor", Text: text, At: at} }

func TestAnAnswerIsTheFirstSupervisorEntryAfterTheHarnessTookTheMessage(t *testing.T) {
	entries := []Entry{
		sup("the earlier turn goes on", "2026-11-17T04:41:30Z"),
		op("status?\n", "2026-11-17T04:42:00Z"),
		sup("t1 is in review", "2026-11-17T04:42:05Z"),
		sup("and t2 waits", "2026-11-17T04:42:09Z"),
	}
	got := Answers(entries, []Sent{{ID: 7, Body: "status?", CreatedAt: "2026-11-17T04:41:00Z"}})
	if len(got) != 1 || got[7] != "2026-11-17T04:42:05Z" {
		t.Fatalf("answers = %v; the supervisor words from before the message must not answer it", got)
	}
}

func TestAMessageTheHarnessHasNotRecordedIsNotAnswered(t *testing.T) {
	entries := []Entry{sup("old news", "2026-11-17T04:40:00Z"), sup("more news", "2026-11-17T04:43:00Z")}
	if got := Answers(entries, []Sent{{ID: 1, Body: "status?", CreatedAt: "2026-11-17T04:41:00Z"}}); len(got) != 0 {
		t.Fatalf("answers = %v, want none: the model has not taken it", got)
	}
}

func TestTwoMessagesBeforeOneReplyAreBothAnsweredByIt(t *testing.T) {
	entries := []Entry{
		op("one", "2026-11-17T04:42:00Z"),
		op("two", "2026-11-17T04:42:10Z"),
		sup("both seen", "2026-11-17T04:42:20Z"),
	}
	got := Answers(entries, []Sent{
		{ID: 1, Body: "one", CreatedAt: "2026-11-17T04:41:59Z"},
		{ID: 2, Body: "two", CreatedAt: "2026-11-17T04:42:09Z"},
	})
	if got[1] != "2026-11-17T04:42:20Z" || got[2] != "2026-11-17T04:42:20Z" {
		t.Fatalf("answers = %v", got)
	}
}

func TestTheSecondMessageWaitsForItsOwnReplyWhenTheFirstWasAnsweredBeforeIt(t *testing.T) {
	entries := []Entry{
		op("one", "2026-11-17T04:42:00Z"),
		sup("first reply", "2026-11-17T04:42:05Z"),
		op("two", "2026-11-17T04:42:10Z"),
	}
	got := Answers(entries, []Sent{
		{ID: 1, Body: "one", CreatedAt: "2026-11-17T04:41:59Z"},
		{ID: 2, Body: "two", CreatedAt: "2026-11-17T04:42:09Z"},
	})
	if got[1] != "2026-11-17T04:42:05Z" {
		t.Fatalf("answers = %v", got)
	}
	if _, ok := got[2]; ok {
		t.Fatalf("the first reply answered the second message: %v", got)
	}
}

func TestTheSameTextSentTwiceTakesTheTranscriptEntriesInOrder(t *testing.T) {
	entries := []Entry{
		op("ping", "2026-11-17T04:00:01Z"),
		sup("pong", "2026-11-17T04:00:02Z"),
		op("ping", "2026-11-17T05:00:01Z"),
	}
	got := Answers(entries, []Sent{
		{ID: 1, Body: "ping", CreatedAt: "2026-11-17T04:00:00Z"},
		{ID: 2, Body: "ping", CreatedAt: "2026-11-17T05:00:00Z"},
	})
	if got[1] != "2026-11-17T04:00:02Z" {
		t.Fatalf("answers = %v", got)
	}
	if _, ok := got[2]; ok {
		t.Fatalf("an old reply answered the new ping: %v", got)
	}
}

func TestAnEntryOlderThanTheMessageIsNeverItsTwin(t *testing.T) {
	entries := []Entry{op("ping", "2026-11-17T03:00:00Z"), sup("pong", "2026-11-17T03:00:05Z")}
	if got := Answers(entries, []Sent{{ID: 1, Body: "ping", CreatedAt: "2026-11-17T04:00:00Z"}}); len(got) != 0 {
		t.Fatalf("answers = %v", got)
	}
}

func TestPairGivesTheOldestInputTheOldestEntryAndLeavesALaterOneUnmatched(t *testing.T) {
	entries := []Entry{op("continue", "2026-11-17T04:00:01Z")}
	got := Pair(entries, []Sent{
		{ID: 2, Body: "continue", CreatedAt: "2026-11-17T05:00:00Z"},
		{ID: 1, Body: "continue", CreatedAt: "2026-11-17T04:00:00Z"},
	})
	if i, ok := got[1]; !ok || i != 0 {
		t.Fatalf("pairs = %v, want input 1 on entry 0", got)
	}
	if _, ok := got[2]; ok {
		t.Fatalf("pairs = %v, the newer input took an entry older than itself", got)
	}
}
