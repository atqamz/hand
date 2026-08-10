//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockOffset = ^uint32(0)

// Lock takes an exclusive advisory lock on file. With block false it returns
// ErrBusy immediately if another process already holds it, instead of waiting.
func Lock(file *os.File, block bool) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !block {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, lockOffset, new(windows.Overlapped))
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return ErrBusy
		}
		return err
	}
	return nil
}

// Unlock releases a lock taken by Lock.
func Unlock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, lockOffset, new(windows.Overlapped))
}
