//go:build unix

package cli

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/atqamz/hand/internal/state"
)

func stopRoot(pid int, marker string) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if !rootAlive(pid, marker) {
			return nil
		}
		if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		grace := stopGrace
		if sig == syscall.SIGKILL {
			grace = killGrace
		}
		if waitGone(pid, marker, grace) {
			return nil
		}
	}
	return fmt.Errorf("%w: root process %d is still alive after SIGKILL", state.ErrConflict, pid)
}
