package watcher

import "time"

// The pause between an activity check's two pane reads. Long enough for a genuinely active terminal's
// streaming indicator to tick over, short enough not to meaningfully delay a report a supervisor is
// waiting on.
const ParkedActivityCheckWait = 3 * time.Second

// How much of the pane tail the two reads compare. Everything a working harness redraws - its spinner,
// its elapsed counter, the text streaming above them - sits at the bottom, so a longer tail would only
// add settled scrollback that never differs.
const parkedActivityReadLines = 20

// PaneReader is the herdr surface a pane-activity check needs, narrowed the way limitPane is so a test
// can drive the comparison without a herdr daemon.
type PaneReader interface {
	PaneRead(paneID string, lines int) (string, error)
}

// sleep is injected rather than called directly so the comparison stays pure and fast to test.
func paneOutputChanged(client PaneReader, paneID string, wait time.Duration, sleep func(time.Duration)) (bool, error) {
	before, err := client.PaneRead(paneID, parkedActivityReadLines)
	if err != nil {
		return false, err
	}
	sleep(wait)
	after, err := client.PaneRead(paneID, parkedActivityReadLines)
	if err != nil {
		return false, err
	}
	return before != after, nil
}

// ConfirmParked cross-checks a time-bound parked verdict against live pane output, so a worker still
// streaming tokens is not called parked merely for having written no recent report line
// (atqamz/hand#364). Changed pane bytes are activity evidence only, never a report or an outcome.
func ConfirmParked(naive bool, client PaneReader, paneID string, wait time.Duration, sleep func(time.Duration)) bool {
	// A check that cannot run must not suppress evidence that predates it, so every degraded path hands
	// back the time-bound verdict untouched.
	if !naive || client == nil || paneID == "" {
		return naive
	}
	changed, err := paneOutputChanged(client, paneID, wait, sleep)
	if err != nil {
		return naive
	}
	return naive && !changed
}
