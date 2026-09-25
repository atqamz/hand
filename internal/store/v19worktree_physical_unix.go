//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func CanonicalV19WorktreePhysicalIdentity(path string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("file identity metadata is absent")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("file identity metadata has type %T, want *syscall.Stat_t", info.Sys())
	}
	sec, nsec, err := canonicalV19FileBirthTime(path, info, stat)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("unix-v2:ino=%016x:btime=%d.%09d", uint64(stat.Ino), sec, nsec), nil
}
