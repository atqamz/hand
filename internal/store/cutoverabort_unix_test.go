//go:build !windows

package store

import (
	"os"
	"strings"
	"testing"
)

// POSIX keeps a stale connection on the replaced bridge inode; Windows refuses the replace instead (see the held-bridge test).
func TestAbortLegacyV18CutoverFreezeLeavesStaleConnectionOnFrozenBridge(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	stale := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = stale.Close() }()
	if _, err := stale.Exec(`SELECT COUNT(*) FROM meta`); err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Exec(`INSERT INTO meta(key, value) VALUES('stale', 'write')`); err == nil || !strings.Contains(err.Error(), legacyV18CutoverFreezeAbortMessage) {
		t.Fatalf("stale bridge connection write after abort = %v, want the freeze guard", err)
	}
	if _, err := os.Lstat(Path(home) + "-journal"); !os.IsNotExist(err) {
		t.Fatalf("stale bridge connection left a journal beside the restored DB: %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.SourceSHA256 {
		t.Fatalf("restored DB after a stale write = %s, %v; want %s", got, err, bridge.SourceSHA256)
	}
}
