//go:build !windows

package fsidentity

import (
	"fmt"
	"os"
	"syscall"
)

func rawIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("directory identity metadata is unavailable")
	}
	return fmt.Sprintf("unix-v1:dev=%016x:ino=%016x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
