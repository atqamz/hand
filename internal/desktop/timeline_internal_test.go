package desktop

import (
	"slices"
	"testing"
	"time"
)

func TestDividersSitAboveTheFirstEntryOfEachDay(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	defer func() { time.Local = old }()
	entries := []Entry{
		{Role: "supervisor", No: 6, At: "2026-11-01T00:00:00Z"},
		{Role: "supervisor", No: 5, At: "2026-10-31T23:59:59Z"},
		{Role: "operator", No: 4, At: "2026-10-31T09:00:00Z"},
		{Role: "hand", At: "not a time"},
		{Role: "operator", No: 2, At: "2026-10-30T23:00:00Z"},
		{Role: "operator", No: 1, At: "2026-10-30T01:00:00Z"},
	}
	var got []string
	var dated []bool
	for _, r := range timelineRows(entries) {
		if r.Day != "" {
			got = append(got, r.Day)
			continue
		}
		got = append(got, string(rune('0'+r.Entry.No)))
		dated = append(dated, r.Dated)
	}
	want := []string{"1", "2", "0", "Sat, Oct 31", "4", "5", "Sun, Nov 1", "6"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	if want := []bool{true, true, true, false, false, false}; !slices.Equal(dated, want) {
		t.Fatalf("dated = %v, want %v: only entries before the first divider carry a date", dated, want)
	}
}

func TestAConversationOfOneDayHasNoDividerAndKeepsItsDates(t *testing.T) {
	rows := timelineRows([]Entry{{No: 2, At: "2026-10-31T10:00:00Z"}, {No: 1, At: "2026-10-31T09:00:00Z"}})
	if len(rows) != 2 || !rows[0].Dated || !rows[1].Dated {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestElapsedIsTheBoardsMinutesAndHours(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "<1m"}, {59 * time.Second, "<1m"}, {time.Minute, "1m"}, {59 * time.Minute, "59m"},
		{time.Hour, "1h 0m"}, {61 * time.Minute, "1h 1m"}, {-time.Hour, "<1m"},
	} {
		if got := elapsed(now.Add(-tc.ago).Format(time.RFC3339), now); got != tc.want {
			t.Errorf("%v ago = %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := elapsed("", now); got != "" {
		t.Errorf("no time = %q", got)
	}
}

func TestTheWorkingTextNamesTheTurnTheLastOutputAndWhatItWorksOn(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	defer func() { time.Local = old }()
	now := time.Date(2026, 11, 1, 12, 4, 0, 0, time.UTC)
	for _, tc := range []struct {
		w    Working
		want string
	}{
		{Working{Since: "2026-11-01T12:00:00Z", On: "since the turn began"}, "4m · since the turn began"},
		{Working{Since: "2026-11-01T10:00:00Z", Last: "2026-11-01T12:03:00Z", On: "x"}, "2h 4m · last output 1m ago · x"},
		{Working{Since: "2026-11-01T12:00:00Z", Input: 7, From: "2026-11-01T11:50:00Z", On: "x"}, "4m · on your 11:50 message"},
	} {
		if got := workingText(&tc.w, now); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestAFoldedWakeNamesItsFirstAndLastMinute(t *testing.T) {
	old := time.Local
	time.Local = time.UTC
	defer func() { time.Local = old }()
	e := Entry{Role: "wakes", At: "2026-11-01T12:29:40Z", Wakes: []Entry{{At: "2026-11-01T12:28:10Z"}, {At: "2026-11-01T12:29:40Z"}}}
	if got := wakeSpan(e); got != "12:28–12:29" {
		t.Fatalf("span = %q", got)
	}
}
