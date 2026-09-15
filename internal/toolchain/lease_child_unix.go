//go:build !windows

package toolchain

import (
	"os"
	"os/exec"
)

func startChildWithLease(cmd *exec.Cmd, lock *os.File) error {
	cmd.ExtraFiles = append(cmd.ExtraFiles, lock)
	return cmd.Start()
}
