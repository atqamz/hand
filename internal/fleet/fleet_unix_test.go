//go:build unix

package fleet_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestListSkipsABookmarkThatVanishesMidScan(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	first, second := f.ID, "f"+strings.Repeat("f", 12)
	if second <= first {
		t.Fatalf("test ids out of order: %s %s", first, second)
	}
	index := fleet.IndexDir(root)
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(index, second), []byte(dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(index, first)
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	type result struct {
		entries []fleet.Entry
		err     error
	}
	done := make(chan result)
	go func() {
		entries, err := fleet.List(root)
		done <- result{entries, err}
	}()
	w, err := os.OpenFile(pipe, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(index, second)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(dir + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || len(got.entries) != 1 || got.entries[0].ID != first || got.entries[0].State != "ok" {
		t.Fatalf("list = %+v, %v; want only %s", got.entries, got.err, first)
	}
}

func TestSkillsSaveSucceedsWhenTheStateFolderCannotBeSynced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder modes")
	}
	skills := fleet.Skills{Base: t.TempDir()}
	dir := filepath.Join(skills.Base, fleet.SkillRoots[1], "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := fleet.SyncDir(dir); err == nil {
		t.Skip("this folder syncs without read access")
	}
	if err := skills.Save(map[string]fleet.Managed{"a": {}}); err != nil {
		t.Fatalf("save with an unsyncable folder: %v", err)
	}
}
