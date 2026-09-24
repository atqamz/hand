//go:build e2e && darwin

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type unixBackgroundProcess struct {
	cmd     *exec.Cmd
	stopErr error
	stopped bool
}

func startBackgroundProcess(cmd *exec.Cmd) (backgroundProcess, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixBackgroundProcess{cmd: cmd}, nil
}

func (p *unixBackgroundProcess) stop() {
	if p.stopped {
		return
	}
	p.stopped = true
	group := p.cmd.Process.Pid
	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		p.stopErr = err
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		active, err := activeBackgroundGroupMembers(group)
		if err != nil {
			p.stopErr = err
			return
		}
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			p.stopErr = fmt.Errorf("background hand group still has %d active processes", active)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func activeBackgroundGroupMembers(group int) (int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", group)
	if err != nil {
		return 0, err
	}
	active := 0
	for _, process := range processes {
		if process.Eproc.Pgid == int32(group) && process.Proc.P_stat != 5 {
			active++
		}
	}
	return active, nil
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
			return errors.Join(errno, p.cmd.Wait(), p.stopErr), false
		}
		if info[0] != 0 {
			p.stop()
			return errors.Join(p.cmd.Wait(), p.stopErr), false
		}
		select {
		case <-timer.C:
			p.stop()
			return errors.Join(p.cmd.Wait(), p.stopErr), true
		case <-ticker.C:
		}
	}
}

func (p *unixBackgroundProcess) close() error { return p.stopErr }

func TestBackgroundWaitJoinsGroupWriterBeforeReturning(t *testing.T) {
	for index := range 12 {
		leader := exec.Command("/bin/sleep", "0.1")
		process, err := startBackgroundProcess(leader)
		if err != nil {
			t.Fatal(err)
		}
		base := t.TempDir()
		ready := filepath.Join(base, "ready")
		writer := exec.Command("/bin/sh", "-c", `printf ready > "$READY"; while :; do printf x >> "$TARGET"; done`)
		writer.Env = append(os.Environ(), "READY="+ready, "TARGET="+filepath.Join(base, "writer"))
		writer.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
		if err := writer.Start(); err != nil {
			process.stop()
			_ = leader.Wait()
			t.Fatal(err)
		}
		leaderReaped, writerReaped := false, false
		t.Cleanup(func() {
			if !leaderReaped {
				process.stop()
				_ = leader.Wait()
			}
			if !writerReaped {
				_ = writer.Process.Kill()
				_ = writer.Wait()
			}
		})
		waitForDescendantFile(t, ready)
		var leaderInfo [128]byte
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(leader.Process.Pid), uintptr(unsafe.Pointer(&leaderInfo[0])), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno != 0 {
			t.Fatal(errno)
		}
		if err, timedOut := process.wait(time.Second); err != nil || timedOut {
			t.Fatalf("leader wait = %v, timeout %v", err, timedOut)
		}
		leaderReaped = true
		var writerInfo [128]byte
		_, _, errno = syscall.Syscall6(syscall.SYS_WAITID, 1, uintptr(writer.Process.Pid), uintptr(unsafe.Pointer(&writerInfo[0])), syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT, 0, 0)
		if errno != 0 {
			t.Fatal(errno)
		}
		_ = writer.Wait()
		writerReaped = true
		if writerInfo[0] == 0 {
			t.Fatalf("group writer %d still active after leader wait", index)
		}
	}
}
