//go:build unix

package update

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestChildKillsTheWholeProcessGroupWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	script, pidFile := filepath.Join(dir, "hand"), filepath.Join(dir, "pid")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 300 &\necho $! > \"$PIDFILE\"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := childOutput(ctx, Options{Target: script, Env: []string{"PATH=" + os.Getenv("PATH"), "PIDFILE=" + pidFile}}, dir, "init")
		done <- err
	}()
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	if pid == 0 {
		t.Fatal("the child never started its helper")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled child reported success")
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	t.Fatalf("helper %d outlived the cancelled child", pid)
}
