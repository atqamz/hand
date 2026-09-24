//go:build e2e && linux

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	active := 0
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			return 0, fmt.Errorf("malformed process stat for %s", entry.Name())
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 3 {
			return 0, fmt.Errorf("malformed process stat for %s", entry.Name())
		}
		processGroup, err := strconv.Atoi(fields[2])
		if err != nil {
			return 0, err
		}
		if processGroup == group && fields[0] != "Z" && fields[0] != "X" {
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
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, p.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			p.stop()
			return errors.Join(err, p.cmd.Wait(), p.stopErr), false
		}
		if info.Signo != 0 {
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
		writer := exec.Command("/bin/sh", "-c", `printf ready > "$READY"; while :; do printf x >> "$TARGET"; /bin/sleep 0.005; done`)
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
		var leaderInfo unix.Siginfo
		if err := unix.Waitid(unix.P_PID, leader.Process.Pid, &leaderInfo, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
			t.Fatal(err)
		}
		if err, timedOut := process.wait(time.Second); err != nil || timedOut {
			t.Fatalf("leader wait = %v, timeout %v", err, timedOut)
		}
		leaderReaped = true
		var writerInfo unix.Siginfo
		if err := unix.Waitid(unix.P_PID, writer.Process.Pid, &writerInfo, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
			t.Fatal(err)
		}
		_ = writer.Wait()
		writerReaped = true
		if writerInfo.Signo == 0 {
			t.Fatalf("group writer %d still active after leader wait", index)
		}
	}
}
