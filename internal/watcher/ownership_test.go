package watcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAcquireRefusesASecondWatcherAndNamesTheIncumbent(t *testing.T) {
	home := t.TempDir()

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer ownership.Release()

	// Two flocks from one process genuinely conflict on Linux: each os.OpenFile is
	// its own open file description, which is what an flock is held against.
	_, err = Acquire(home, false)
	if err == nil {
		t.Fatal("second Acquire succeeded, want a refusal while a watcher is attached")
	}
	if !strings.Contains(err.Error(), ErrAttached.Error()) {
		t.Fatalf("got %v, want it to wrap ErrAttached", err)
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("got %v, want the refusal to name the incumbent pid %d", err, os.Getpid())
	}
	if !strings.Contains(err.Error(), "--takeover") {
		t.Fatalf("got %v, want the refusal to name the remedy", err)
	}
}

func TestAcquireIgnoresAStaleFileWhoseRecordedPidIsDead(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, strconv.Itoa(deadPid(t))+"\n")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over a crashed watcher's leftover pid file: %v, want it to succeed - a lock that refuses forever after a crash locks this home out of watching itself", err)
	}
	defer ownership.Release()

	if got := readOwnerFile(t, home); got != strconv.Itoa(os.Getpid())+"\n" {
		t.Fatalf("got %q, want the leftover pid replaced with this process's own", got)
	}
}

// Half of the same guarantee: the stale case must not need --takeover, and must
// also not break under it.
func TestAcquireTakesOverAStaleFileWhoseRecordedPidIsDead(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, strconv.Itoa(deadPid(t))+"\n")

	ownership, err := Acquire(home, true)
	if err != nil {
		t.Fatalf("Acquire --takeover over a crashed watcher's leftover pid file: %v", err)
	}
	ownership.Release()
}

func TestAcquireIgnoresAStaleFileHoldingGarbage(t *testing.T) {
	home := t.TempDir()
	writeOwnerFile(t, home, "not-a-pid")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over an unparseable pid file: %v", err)
	}
	ownership.Release()
}

func TestReleaseClearsTheRecordedPid(t *testing.T) {
	home := t.TempDir()

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ownership.Release()

	if got := readOwnerFile(t, home); got != "" {
		t.Fatalf("got %q in %s after release, want it empty - a pid left behind names a process that has exited", got, OwnerPath(home))
	}

	ownership, err = Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	ownership.Release()
}

// Returns a pid that was real and is now gone, so a test about a crashed watcher is about an
// actually dead process rather than an invented number that might collide with a live one.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run a throwaway process to consume a pid: %v", err)
	}
	return cmd.Process.Pid
}

func writeOwnerFile(t *testing.T, home, content string) {
	t.Helper()
	path := OwnerPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOwnerFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(OwnerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A stale advisory pid that happens to be a live innocent process must never be
// attacked: when the kernel lock is free, Acquire ignores it entirely.
func TestAcquireReplacesStaleLivePIDAndLeavesTheInnocentProcessAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support the signal-zero liveness probe")
	}
	home := t.TempDir()
	innocent := exec.Command("sleep", "3600")
	if err := innocent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = innocent.Process.Kill()
		_ = innocent.Wait()
	}()
	writeOwnerFile(t, home, strconv.Itoa(innocent.Process.Pid)+"\n")

	ownership, err := Acquire(home, false)
	if err != nil {
		t.Fatalf("Acquire over a stale live pid: %v", err)
	}
	ownership.Release()

	// A zero signal is a liveness probe, never a delivered signal; it proves the
	// innocent process was not terminated by any takeover handling.
	if err := innocent.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("innocent process no longer exists after stale-pid handling: %v", err)
	}
	if got := readOwnerFile(t, home); got != "" {
		t.Fatalf("owner file = %q after release, want it clear - not the innocent process's pid", got)
	}
}

// Simulates a holder that has not (or no longer) published a coherent routing
// record: a --takeover contender must perform no destructive action and simply
// wait for the authoritative lock.
func TestContenderWithNoRecordWaitsForTheLock(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(OwnerRecordPath(home)); err != nil {
		t.Fatal(err)
	}

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 3*time.Second, 20*time.Millisecond

	type outcome struct {
		ownership *Ownership
		err       error
	}
	done := make(chan outcome, 1)
	go func() { o, e := Acquire(home, true); done <- outcome{o, e} }()

	// Held, no record: contender waits, does nothing destructive, is not yet owner.
	select {
	case r := <-done:
		t.Fatalf("contender returned early while the lock was held: %v / %v", r.ownership, r.err)
	case <-time.After(100 * time.Millisecond):
	}
	holder.Release()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("contender Acquire after release: %v", r.err)
		}
		r.ownership.Release()
	case <-time.After(3 * time.Second):
		t.Fatal("contender did not acquire once the holder released the lock")
	}
}

// Same expectation with a malformed record: no destructive action, Safe keeps
// waiting, and the lock alone decides ownership.
func TestContenderWithMalformedRecordWaitsForTheLock(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OwnerRecordPath(home), []byte(`{truncated`), 0o644); err != nil {
		t.Fatal(err)
	}

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 3*time.Second, 20*time.Millisecond

	done := make(chan error, 1)
	go func() { _, e := Acquire(home, true); done <- e }()
	select {
	case err := <-done:
		t.Fatalf("contender returned early while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	holder.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("contender Acquire after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("contender did not acquire once the holder released the lock")
	}
}

// If safe takeover cannot complete before the grace bound because the lock
// never releases, the contender fails and the incumbent stays the sole owner -
// there are never two owners.
func TestContenderFailsWithinGraceWithoutCreatingASecondOwner(t *testing.T) {
	home := t.TempDir()
	holder, err := Acquire(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	restoreTakeoverClocks(t)
	takeoverGrace, takeoverPoll = 200*time.Millisecond, 20*time.Millisecond

	_, err = Acquire(home, true)
	if !errors.Is(err, ErrAttached) {
		t.Fatalf("takeover contender = %v, want ErrAttached when the lock never releases", err)
	}
	// The holder still owns the single lock.
	if _, err := Acquire(home, true); !errors.Is(err, ErrAttached) {
		t.Fatalf("after a failed contender the holder was displaced: %v", err)
	}
}

func restoreTakeoverClocks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		takeoverGrace = 5 * time.Second
		takeoverPoll = 50 * time.Millisecond
	})
}
