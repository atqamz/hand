//go:build unix

package cli

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/state"
)

var updateGrace = 10 * time.Second

func stopOne(pid int, marker string) error {
	if !rootAlive(pid, marker) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if waitGone(pid, marker, updateGrace) {
		return nil
	}
	return fmt.Errorf("%w: process %d is still alive %s after SIGTERM", state.ErrConflict, pid, updateGrace)
}
