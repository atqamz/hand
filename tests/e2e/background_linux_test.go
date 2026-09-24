//go:build e2e && linux

package e2e

import (
	"errors"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, p.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			p.stop()
			return errors.Join(err, p.cmd.Wait()), false
		}
		if info.Signo != 0 {
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
