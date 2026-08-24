package watcher

import (
	"errors"
	"slices"
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

func (r refusingPaneReader) PaneRead(string, int) (string, error) {
	r.t.Fatal("pane was read for a task the time bound never called parked")
	return "", nil
}

func noSleep(time.Duration) {}

func TestConfirmParkedHoldsTheVerdictWhenThePaneStaysStill(t *testing.T) {
	client := &fakePaneReader{reads: []string{"waiting for input", "waiting for input"}}

	if !ConfirmParked(true, client, "wA:pB", ParkedActivityCheckWait, noSleep) {
		t.Fatal("an unchanging pane withdrew the parked verdict, want it held: silence is the whole evidence")
	}
}

func TestConfirmParkedWithdrawsTheVerdictWhenThePaneKeepsPrinting(t *testing.T) {
	client := &fakePaneReader{reads: []string{"esc to interrupt (12s)", "esc to interrupt (15s)"}}

	if ConfirmParked(true, client, "wA:pB", ParkedActivityCheckWait, noSleep) {
		t.Fatal("a streaming pane stayed parked, want the verdict withdrawn (atqamz/hand#364)")
	}
}

func TestConfirmParkedKeepsTheNaiveVerdictWhenThePaneCannotBeRead(t *testing.T) {
	client := &fakePaneReader{err: errors.New("pane_not_found")}

	if !ConfirmParked(true, client, "wA:pB", ParkedActivityCheckWait, noSleep) {
		t.Fatal("a failed read cleared the parked verdict, want it kept: a check that cannot run confirms nothing either way")
	}
}

func TestConfirmParkedKeepsTheNaiveVerdictWithNothingToRead(t *testing.T) {
	if !ConfirmParked(true, nil, "wA:pB", ParkedActivityCheckWait, noSleep) {
		t.Fatal("a nil client cleared the parked verdict, want it kept")
	}
	if !ConfirmParked(true, &fakePaneReader{}, "", ParkedActivityCheckWait, noSleep) {
		t.Fatal("an attempt with no pane cleared the parked verdict, want it kept")
	}
}

func TestConfirmParkedNeverReadsAPaneItHasNothingToConfirm(t *testing.T) {
	if ConfirmParked(false, refusingPaneReader{t}, "wA:pB", ParkedActivityCheckWait, noSleep) {
		t.Fatal("a task the time bound never called parked came back parked")
	}
}

func TestPaneOutputChangedWaitsBetweenItsTwoReads(t *testing.T) {
	client := &fakePaneReader{reads: []string{"first", "second"}}
	var slept []time.Duration
	sleep := func(d time.Duration) {
		slept = append(slept, d)
		client.trace = append(client.trace, "sleep")
	}

	changed, err := paneOutputChanged(client, "wA:pB", ParkedActivityCheckWait, sleep)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("two different reads compared equal")
	}
	if !slices.Equal(slept, []time.Duration{ParkedActivityCheckWait}) {
		t.Fatalf("slept %v, want exactly one wait of %s", slept, ParkedActivityCheckWait)
	}
	if want := []string{"read", "sleep", "read"}; !slices.Equal(client.trace, want) {
		t.Fatalf("trace = %v, want %v: two reads taken at the same instant prove nothing", client.trace, want)
	}
	if !slices.Equal(client.paneIDs, []string{"wA:pB", "wA:pB"}) {
		t.Fatalf("read panes %v, want both reads against wA:pB", client.paneIDs)
	}
	if !slices.Equal(client.lines, []int{parkedActivityReadLines, parkedActivityReadLines}) {
		t.Fatalf("read %v lines, want both reads to take the same %d-line tail", client.lines, parkedActivityReadLines)
	}
}
