package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gone(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestStopKillsTheWorkerProcessGroup(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	if out := fx.h.ok("attempt", "stop", "a1"); !strings.Contains(out, "status: stopped") {
		t.Fatalf("stop = %q", out)
	}
	if !gone(pid) {
		t.Fatalf("worker %d still alive", pid)
	}
	if _, errOut, code := fx.h.run("attempt", "stop", "a1"); code != 3 || !strings.Contains(errOut, "attempt a1 is stopped") {
		t.Fatalf("second stop code=%d stderr=%q", code, errOut)
	}
	if out := fx.h.ok("task", "done", "t1"); !strings.Contains(out, "status: done") {
		t.Fatalf("task done after stop = %q", out)
	}
}

func TestStopNeverSignalsAProcessWithAnotherStartMarker(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.set(func(rt *fakeRuntime) { rt.marker = "1" })
	fx.start()
	pid := fx.rt.lastPID()
	out := fx.h.ok("attempt", "stop", "a1")
	if !strings.Contains(out, "status: exited") || !strings.Contains(out, "root process already gone") {
		t.Fatalf("stop = %q", out)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("a process with another start marker was signalled: %v", err)
	}
}

func TestCleanProtectsLiveWorkersAndUncommittedWork(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 3 || !strings.Contains(errOut, "attempt a1 is running; stop it first") {
		t.Fatalf("clean live code=%d stderr=%q", code, errOut)
	}
	fx.h.ok("attempt", "stop", "a1")
	wt := filepath.Join(fx.h.home, "worktrees", "t1-a1")
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored := fx.rt.addShell(t, wt)
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 3 || !strings.Contains(errOut, "uncommitted changes") {
		t.Fatalf("clean dirty code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(wt, "wip.txt")); err != nil {
		t.Fatalf("uncommitted work lost: %v", err)
	}
	if out := fx.h.ok("attempt", "clean", "--discard", "a1"); !strings.Contains(out, "removed: "+wt) {
		t.Fatalf("clean = %q", out)
	}
	if _, err := os.Stat(wt); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree still there: %v", err)
	}
	if !fx.rt.isClosed(restored) {
		t.Fatal("a terminal left inside the worktree was not closed")
	}
	if out, err := exec.Command("git", "-C", fx.repo, "rev-parse", "--verify", "hand/t1-a1").CombinedOutput(); err != nil {
		t.Fatalf("branch deleted: %s", out)
	}
	if _, errOut, code := fx.h.run("attempt", "clean", "a1"); code != 3 || !strings.Contains(errOut, "already cleaned") {
		t.Fatalf("clean twice code=%d stderr=%q", code, errOut)
	}
}

func TestCleanAfterTheWorktreeVanished(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	if err := os.RemoveAll(filepath.Join(fx.h.home, "worktrees", "t1-a1")); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("attempt", "clean", "a1")
	if out, _ := exec.Command("git", "-C", fx.repo, "worktree", "list").CombinedOutput(); strings.Contains(string(out), "t1-a1") {
		t.Fatalf("stale worktree entry kept: %s", out)
	}
}
