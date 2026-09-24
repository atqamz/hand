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
	return fmt.Sprintf("unix-v1:dev=%016x:ino=%016x", uint64(stat.Dev), uint64(stat.Ino)), nil
}
