//go:build !windows

package store

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func canonicalV19HerdrProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("process PID %d is invalid", pid)
	}
	err := unix.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
