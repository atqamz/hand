package watcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
