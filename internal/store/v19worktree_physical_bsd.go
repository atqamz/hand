//go:build darwin || freebsd || netbsd

package store

import (
	"fmt"
	"os"
	"syscall"
)

func canonicalV19FileBirthTime(path string, info os.FileInfo, stat *syscall.Stat_t) (int64, int64, error) {
	statPath := os.Stat
	if info.Mode()&os.ModeSymlink != 0 {
		statPath = os.Lstat
	}
	current, err := statPath(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read file birth time: %w", err)
	}
	if !os.SameFile(info, current) {
		return 0, 0, fmt.Errorf("file identity changed before birth time capture")
	}
	birth := stat.Birthtimespec
	if birth.Sec < 0 || birth.Sec == 0 && birth.Nsec == 0 {
		return 0, 0, fmt.Errorf("file birth time is unavailable on this filesystem")
	}
	return int64(birth.Sec), int64(birth.Nsec), nil
}
