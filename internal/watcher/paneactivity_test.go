package watcher

import (
	"errors"
	"testing"
	"time"
)

type fakePaneReader struct {
	reads   []string
	err     error
	calls   int
	paneIDs []string
	lines   []int
	trace   []string
}

func (f *fakePaneReader) PaneRead(paneID string, lines int) (string, error) {
	f.calls++
	f.paneIDs = append(f.paneIDs, paneID)
	f.lines = append(f.lines, lines)
	f.trace = append(f.trace, "read")
	if f.err != nil {
		return "", f.err
	}
	if f.calls > len(f.reads) {
		return "", nil
	}
	return f.reads[f.calls-1], nil
}

type refusingPaneReader struct{ t *testing.T }

func noSleep(time.Duration) {}

func (r refusingPaneReader) PaneRead(string, int) (string, error) {
	r.t.Fatal("pane was read for a task the time bound never called parked")
	return "", nil
}

func TestConfirmParkedHoldsTheVerdictWhenThePaneStaysStill(t *testing.T) {
	client := &fakePaneReader{reads: []string{"waiting for input"}}

	if got, _, _ := ConfirmParked(true, "waiting for input", true, client, "wA:pB"); !got {
		t.Fatal("an unchanging pane withdrew the parked verdict, want it held: silence is the whole evidence")
	}
}

func TestConfirmParkedWithdrawsTheVerdictWhenThePaneKeepsPrinting(t *testing.T) {
	client := &fakePaneReader{reads: []string{"esc to interrupt (15s)"}}

	if got, _, _ := ConfirmParked(true, "esc to interrupt (12s)", true, client, "wA:pB"); got {
		t.Fatal("a streaming pane stayed parked, want the verdict withdrawn (atqamz/hand#364)")
	}
}

func TestConfirmParkedKeepsTheNaiveVerdictWhenThePaneCannotBeRead(t *testing.T) {
	client := &fakePaneReader{err: errors.New("pane_not_found")}

	if got, _, _ := ConfirmParked(true, "waiting", true, client, "wA:pB"); !got {
		t.Fatal("a failed read cleared the parked verdict, want it kept: a check that cannot run confirms nothing either way")
	}
}

func TestConfirmParkedKeepsTheNaiveVerdictWithNothingToRead(t *testing.T) {
	if got, _, _ := ConfirmParked(true, "waiting", true, nil, "wA:pB"); !got {
		t.Fatal("a nil client cleared the parked verdict, want it kept")
	}
	if got, _, _ := ConfirmParked(true, "waiting", true, &fakePaneReader{}, ""); !got {
		t.Fatal("an attempt with no pane cleared the parked verdict, want it kept")
	}
}

func TestConfirmParkedNeverReadsAPaneItHasNothingToConfirm(t *testing.T) {
	if got, _, _ := ConfirmParked(false, "", false, refusingPaneReader{t}, "wA:pB"); got {
		t.Fatal("a task the time bound never called parked came back parked")
	}
}

func TestConfirmParkedStoresAndComparesOneSample(t *testing.T) {
	client := &fakePaneReader{reads: []string{"second"}}
	got, sample, observed := ConfirmParked(true, "first", true, client, "wA:pB")
	if got || sample != "second" || !observed {
		t.Fatalf("got=%v sample=%q observed=%v, want false, second, true", got, sample, observed)
	}
}
