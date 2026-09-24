//go:build e2e && !linux && !darwin && !windows

package e2e

import (
	"os/exec"
	"syscall"
	"time"
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
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err, false
	case <-time.After(timeout):
		_ = p.cmd.Process.Kill()
		return <-done, true
	}
}

func (p *unixBackgroundProcess) close() error { return nil }
