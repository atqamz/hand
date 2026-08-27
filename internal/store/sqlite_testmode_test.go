//go:build test

package store

import "testing"

func TestOpenUsesCheapDurabilityInTestBuild(t *testing.T) {
	db, _ := openTemp(t)

	var journalMode string
	if err := db.sql.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "memory" {
		t.Fatalf("journal mode = %q, want memory", journalMode)
	}

	var synchronous int
	if err := db.sql.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 0 {
		t.Fatalf("synchronous = %d, want 0", synchronous)
	}
}
