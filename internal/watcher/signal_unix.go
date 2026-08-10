//go:build !windows

package watcher

import (
	"errors"
	"syscall"
)

// Asks pid to exit gracefully. A pid that is already gone is not a failure:
// the caller only wants the lock released, and a dead process has already
// released it.
func signalTerminate(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
