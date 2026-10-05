//go:build unix

package flock

import (
	"errors"
	"os"
	"syscall"
)

func Release(f *os.File) error { return f.Close() }

func Lock(f *os.File, wait bool) (bool, error) {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), how)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
