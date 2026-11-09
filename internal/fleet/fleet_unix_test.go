//go:build unix

package fleet_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func TestALegacyBookmarkStillReadsWhenTheIndexCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores a read-only folder")
	}
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	legacy := filepath.Join(root, "fleets")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, f.ID), []byte(dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index := fleet.IndexDir(root)
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(index, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(index, 0o700) })
	if got, err := fleet.Home(root, f.ID); err != nil || got != dir {
		t.Fatalf("home = %q, %v; want the legacy bookmark %s", got, err, dir)
	}
	entries, err := fleet.List(root)
	if err != nil || len(entries) != 1 || entries[0].Home != dir {
		t.Fatalf("list = %+v, %v; want the legacy bookmark %s", entries, err, dir)
	}
	if _, err := os.Stat(filepath.Join(legacy, f.ID)); err != nil {
		t.Fatalf("legacy bookmark was moved or removed: %v", err)
	}
}

func TestListWorksWhenTheSharedFolderIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores a read-only folder")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	entries, err := fleet.List(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("list of a read-only folder = %+v, %v", entries, err)
	}
}
