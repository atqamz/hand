package watcher

// How much of the pane tail the two reads compare. Everything a working harness redraws - its spinner,
// its elapsed counter, the text streaming above them - sits at the bottom, so a longer tail would only
// add settled scrollback that never differs.
const parkedActivityReadLines = 20

// PaneReader is the herdr surface a pane-activity check needs, narrowed the way limitPane is so a test
// can drive the comparison without a herdr daemon.
type PaneReader interface {
	PaneRead(paneID string, lines int) (string, error)
}

func readPaneSample(client PaneReader, paneID string) (string, error) {
	return client.PaneRead(paneID, parkedActivityReadLines)
}

func ConfirmParked(naive bool, previous string, hasPrevious bool, client PaneReader, paneID string) (bool, string, bool) {
	// A check that cannot run must not suppress evidence that predates it, so every degraded path hands
	// back the time-bound verdict untouched.
	if client == nil || paneID == "" {
		return naive, "", false
	}
	current, err := readPaneSample(client, paneID)
	if err != nil {
		return naive, "", false
	}
	if !hasPrevious {
		return false, current, true
	}
	if !naive {
		return false, current, true
	}
	return naive && current == previous, current, true
}
