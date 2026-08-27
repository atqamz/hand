//go:build test

package registry

import (
	"path/filepath"
	"testing"
)

func TestOpenUsesCheapDurabilityInTestBuild(t *testing.T) {
	db, err := open(filepath.Join(t.TempDir(), "registry.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "memory" {
		t.Fatalf("journal mode = %q, want memory", journalMode)
	}

	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 0 {
		t.Fatalf("synchronous = %d, want 0", synchronous)
	}
}
