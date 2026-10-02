//go:build unix

package cli_test

import (
	"os/exec"
	"syscall"
)

func killGroup(cmd *exec.Cmd, hard bool) {
	sig := syscall.SIGHUP
	if hard {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
