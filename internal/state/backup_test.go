package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoreBackupMakesAReadableCopy(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hand.db.20261002T010203")
	if err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	cp, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if f, err := cp.Fleet(ctx); err != nil || f.Name != "alpha" {
		t.Fatalf("copy fleet = %+v, %v", f, err)
	}
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("a second backup to the same path succeeded")
	}
}
