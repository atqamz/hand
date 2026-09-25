//go:build !windows && !linux && !darwin && !freebsd && !netbsd

package store

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

func canonicalV19FileBirthTime(string, os.FileInfo, *syscall.Stat_t) (int64, int64, error) {
	return 0, 0, fmt.Errorf("file birth time is unavailable on %s", runtime.GOOS)
}
