//go:build windows

package watcher

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

const (
	takeoverHelperEnv = "HAND_WATCHER_TAKEOVER_HELPER"
	takeoverHomeEnv   = "HAND_WATCHER_TAKEOVER_HOME"
)

func TestAcquireTakeoverReplacesLiveWatcherOnWindows(t *testing.T) {
	if os.Getenv(takeoverHelperEnv) == "1" {
		runTakeoverHelper(t)
		return
	}

	home := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestAcquireTakeoverReplacesLiveWatcherOnWindows$")
	child.Env = append(os.Environ(), takeoverHelperEnv+"=1", takeoverHomeEnv+"="+home)
	if err := child.Start(); err != nil {
		t.Fatalf("start takeover helper: %v", err)
	}
	t.Cleanup(func() {
		if child.ProcessState != nil {
			return
		}
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	waitForOwnerPID(t, home, child.Process.Pid)
	ownership, err := Acquire(home, true)
	if err != nil {
		t.Fatalf("replacement Acquire: %v", err)
	}

	if got := readOwnerFile(t, home); got != strconv.Itoa(os.Getpid())+"\n" {
		t.Fatalf("got %q in owner file, want replacement pid %d", got, os.Getpid())
	}
	ownership.Release()
	if err := child.Wait(); err != nil {
		t.Fatalf("takeover helper: %v", err)
	}
}

func runTakeoverHelper(t *testing.T) {
	home := os.Getenv(takeoverHomeEnv)
	if home == "" {
		t.Fatal("takeover helper home is empty")
	}
	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("helper Acquire: %v", err)
	}
	requested := ownership.TakeoverRequested()
	if requested == nil {
		t.Fatal("helper has no takeover request channel")
	}
	<-requested
	ownership.Release()
}

func waitForOwnerPID(t *testing.T, home string, pid int) {
	t.Helper()
	want := strconv.Itoa(pid) + "\n"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(OwnerPath(home))
		if err == nil && string(data) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(OwnerPath(home))
	t.Fatalf("timed out waiting for owner pid %d; owner file contains %q", pid, data)
}
