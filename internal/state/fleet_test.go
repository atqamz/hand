package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetIdentityIsCreatedOnceAndCanBeRenamed(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.Fleet(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fleet before create = %v, want ErrNotFound", err)
	}
	f, err := s.CreateFleet(ctx, "yes2games")
	if err != nil || !FleetID.MatchString(f.ID) || f.Name != "yes2games" {
		t.Fatalf("create = %+v, %v", f, err)
	}
	if _, err := s.CreateFleet(ctx, "other"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create = %v, want ErrConflict", err)
	}
	r, err := s.RenameFleet(ctx, "Yes 2 Games")
	if err != nil || r.ID != f.ID || r.Name != "Yes 2 Games" {
		t.Fatalf("rename = %+v, %v", r, err)
	}
	if got, err := s.Fleet(ctx); err != nil || got != r {
		t.Fatalf("fleet = %+v, %v", got, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 65), "a\nb", "\xff"} {
		if _, err := s.RenameFleet(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rename %q = %v, want ErrInvalid", bad, err)
		}
	}
	other, _ := openTest(t)
	if g, err := other.CreateFleet(ctx, "yes2games"); err != nil || g.ID == f.ID {
		t.Fatalf("second home = %+v, %v; want a different id", g, err)
	}
}

func TestOpenEscapesURIMetacharactersInThePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c%41d?b#e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hand.db")
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFleet(context.Background(), "odd"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database is not at %s: %v", path, err)
	}
	again, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if f, err := again.Fleet(context.Background()); err != nil || f.Name != "odd" {
		t.Fatalf("reopened fleet = %+v, %v", f, err)
	}
}
