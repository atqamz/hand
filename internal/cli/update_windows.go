package cli

import (
	"fmt"

	"github.com/atqamz/hand/internal/state"
)

func stopOne(pid int, marker string) error {
	if !rootAlive(pid, marker) {
		return nil
	}
	terminate(uint32(pid), marker)
	if waitGone(pid, marker, killGrace) {
		return nil
	}
	return fmt.Errorf("%w: process %d is still alive after TerminateProcess", state.ErrConflict, pid)
}
