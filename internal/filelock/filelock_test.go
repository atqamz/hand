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
