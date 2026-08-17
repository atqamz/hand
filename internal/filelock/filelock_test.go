package filelock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockThenUnlockAllowsARelock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	if err := Lock(file, false); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := Unlock(file); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := Lock(file, false); err != nil {
		t.Fatalf("relock after unlock: %v", err)
	}
	if err := Unlock(file); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

func TestNonblockingLockReportsErrBusyAgainstAnotherHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if err := Lock(first, false); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	if err := Lock(second, false); err != ErrBusy {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

func TestUnlockThenAnotherHandleCanLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if err := Lock(first, false); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := Unlock(first); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	second, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if err := Lock(second, false); err != nil {
		t.Fatalf("a distinct handle on the same pathname must acquire after unlock: %v", err)
	}
}

// The regression guard against the split-inode reasoning in
// docs/adr/lock-pathnames-are-permanent-rendezvous-points.md: what other
// processes find is the pathname, not the holder's original handle.
func TestLockSurvivesReopeningTheSamePathnameInTheHoldingProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if err := Lock(first, false); err != nil {
		t.Fatalf("first lock: %v", err)
	}

	reopened, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()

	if err := Lock(reopened, false); err != ErrBusy {
		t.Fatalf("got %v, want ErrBusy: a second open file description on the same pathname still conflicts", err)
	}
}
