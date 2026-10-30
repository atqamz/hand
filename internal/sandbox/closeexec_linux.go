//go:build linux

package sandbox

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

var procSelfFD = "/proc/self/fd"

func CloseOnExec() error {
	dir, err := os.Open(procSelfFD)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
			return err
		}
	}
	return nil
}
