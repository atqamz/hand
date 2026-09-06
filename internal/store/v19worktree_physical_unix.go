//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

func canonicalV19WorktreePhysicalIdentity(_ string, info os.FileInfo) (string, error) {
	if info == nil {
		return "", fmt.Errorf("file identity metadata is absent")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("file identity metadata has type %T, want *syscall.Stat_t", info.Sys())
	}
	return fmt.Sprintf("unix-v1:dev=%016x:ino=%016x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
