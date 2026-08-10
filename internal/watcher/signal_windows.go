//go:build windows

package watcher

import "errors"

// signalTerminate's only outcome on windows: SIGTERM has no equivalent there,
// and atqamz/hand#201 tracks the graceful-shutdown design a real takeover
// needs instead of a forceful TerminateProcess kill.
var errTakeoverUnsupported = errors.New(
	"--takeover is not supported on windows yet (atqamz/hand#201): stop the incumbent hand watch process yourself and retry")

func signalTerminate(pid int) error {
	return errTakeoverUnsupported
}
