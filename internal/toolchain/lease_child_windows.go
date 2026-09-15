//go:build windows

package toolchain

import (
	"os"
	"os/exec"
)

func startChildWithLease(cmd *exec.Cmd, _ *os.File) error {
	return cmd.Start()
}
