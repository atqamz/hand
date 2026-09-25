//go:build linux

package store

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func canonicalV19FileBirthTime(path string, info os.FileInfo, stat *syscall.Stat_t) (int64, int64, error) {
	flags := 0
	if info.Mode()&os.ModeSymlink != 0 {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}
	var statx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, flags, unix.STATX_INO|unix.STATX_BTIME, &statx); err != nil {
		return 0, 0, fmt.Errorf("read file birth time: %w", err)
	}
	if statx.Mask&unix.STATX_INO == 0 || statx.Ino != uint64(stat.Ino) || unix.Mkdev(statx.Dev_major, statx.Dev_minor) != uint64(stat.Dev) {
		return 0, 0, fmt.Errorf("file identity changed before birth time capture")
	}
	if statx.Mask&unix.STATX_BTIME == 0 {
		return 0, 0, fmt.Errorf("file birth time is unavailable on this filesystem")
	}
	return statx.Btime.Sec, int64(statx.Btime.Nsec), nil
}
