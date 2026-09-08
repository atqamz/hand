//go:build windows

package store

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func canonicalV19HerdrProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("process PID %d is invalid", pid)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		_ = windows.CloseHandle(handle)
		return true, nil
	}
	switch {
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return false, nil
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return true, nil
	default:
		return false, err
	}
}
