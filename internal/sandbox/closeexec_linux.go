//go:build linux

package sandbox

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func CloseOnExec() {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 3 {
			continue
		}
		unix.CloseOnExec(fd)
	}
}
