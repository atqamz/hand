//go:build darwin || freebsd || netbsd

package store

import (
	"fmt"
	"os"
	"syscall"
)

func canonicalV19FileBirthTime(_ string, _ os.FileInfo, stat *syscall.Stat_t) (int64, int64, error) {
	birth := stat.Birthtimespec
	if birth.Sec < 0 || birth.Sec == 0 && birth.Nsec == 0 {
		return 0, 0, fmt.Errorf("file birth time is unavailable on this filesystem")
	}
	return int64(birth.Sec), int64(birth.Nsec), nil
}
