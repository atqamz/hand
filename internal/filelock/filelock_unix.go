//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// Lock takes an exclusive advisory lock on file. With block false it returns
// ErrBusy immediately if another process already holds it, instead of waiting.
func Lock(file *os.File, block bool) error {
	return lock(file, block, syscall.LOCK_EX)
}

// LockShared takes a shared advisory lock on file. Multiple shared holders may
// coexist, while an exclusive Lock conflicts with all of them.
func LockShared(file *os.File, block bool) error {
	return lock(file, block, syscall.LOCK_SH)
}

func lock(file *os.File, block bool, flags int) error {
	if !block {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
	return nil
}

// Unlock releases a lock taken by Lock.
func Unlock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
