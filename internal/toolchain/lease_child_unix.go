//go:build !windows

package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func startChildWithLease(cmd *exec.Cmd, lock *os.File) error {
	cmd.ExtraFiles = append(cmd.ExtraFiles, lock)
	return cmd.Start()
}

func leaseLockIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("lease lock file identity metadata has type %T", info.Sys())
	}
	return leaseStatIdentity(stat), nil
}

func leaseStatIdentity(stat *syscall.Stat_t) string {
	return fmt.Sprintf("unix-v2:ino=%016x", uint64(stat.Ino))
}
