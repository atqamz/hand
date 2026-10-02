package luvus

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"golang.org/x/sys/windows"
)

const stillActive = 259

func ProcStartMarker(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
		return "", fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return "", err
	}
	if code != stillActive {
		return "", fmt.Errorf("%w: %w", fs.ErrNotExist, windows.ERROR_INVALID_PARAMETER)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), nil
}
