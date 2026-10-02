package luvus

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"golang.org/x/sys/windows"
)

func ProcStartMarker(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
		return "", fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return "", err
	}
	if ev != uint32(windows.WAIT_TIMEOUT) {
		return "", fmt.Errorf("%w: process %d has exited", fs.ErrNotExist, pid)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return "windows:" + strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10), nil
}
