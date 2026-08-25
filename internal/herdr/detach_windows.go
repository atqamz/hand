//go:build windows

package herdr

import (
	"os/exec"
	"syscall"
)

func detachCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
