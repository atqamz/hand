//go:build e2e && darwin

package e2e

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

type unixBackgroundProcess struct {
	cmd *exec.Cmd
}

func startBackgroundProcess(cmd *exec.Cmd) (backgroundProcess, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixBackgroundProcess{cmd: cmd}, nil
}

func (p *unixBackgroundProcess) stop() {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}

func (p *unixBackgroundProcess) wait(timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var info [128]byte
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(p.cmd.Process.Pid), uintptr(unsafe.Pointer(&info[0])), syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			p.stop()
			return errors.Join(errno, p.cmd.Wait()), false
		}
		if info[0] != 0 {
			p.stop()
			return p.cmd.Wait(), false
		}
		select {
		case <-timer.C:
			p.stop()
			return p.cmd.Wait(), true
		case <-ticker.C:
		}
	}
}

func (p *unixBackgroundProcess) close() error { return nil }
