//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func legacyV18CutoverSourceLinkCount(_ *os.File, info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, fmt.Errorf("file identity metadata has type %T, want *syscall.Stat_t", info.Sys())
	}
	return uint64(stat.Nlink), nil
}
