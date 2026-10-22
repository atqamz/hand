//go:build unix

package cli_test

import (
	"os/exec"
	"syscall"
)

func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
