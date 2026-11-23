//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/flock"
)

func TestWatchLockedReadsALockInAReadOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	home := t.TempDir()
	path := filepath.Join(home, "watch.lock")
	if err := os.WriteFile(path, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o755) })
	if held, err := watchLocked(home); err != nil || held {
		t.Fatalf("free lock: held=%v err=%v", held, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer flock.Release(f)
	if ok, err := flock.Lock(f, false); err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	if held, err := watchLocked(home); err != nil || !held {
		t.Fatalf("held lock: held=%v err=%v", held, err)
	}
}
