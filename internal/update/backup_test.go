package update

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

var stamp = time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC)

func newFleet(t *testing.T, root, name string) (string, string) {
	t.Helper()
	home := t.TempDir()
	st, err := state.Open(filepath.Join(home, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.CreateFleet(context.Background(), name)
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Register(root, f.ID, home); err != nil {
		t.Fatal(err)
	}
	return f.ID, home
}

func entries(t *testing.T, root string) []fleet.Entry {
	t.Helper()
	list, err := fleet.List(root)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func binary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hand")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	des, _ := os.ReadDir(dir)
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func TestBackupCopiesEachFleetAndTheBinary(t *testing.T) {
	root := t.TempDir()
	a, _ := newFleet(t, root, "alpha")
	b, _ := newFleet(t, root, "beta")
	target := binary(t, "old hand")
	got, err := Backup(context.Background(), root, target, entries(t, root), stamp)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "backups", a, "hand.db.20261002T010203"), filepath.Join(root, "backups", b, "hand.db.20261002T010203"), filepath.Join(root, "backups", "hand.20261002T010203")}
	slices.Sort(want[:2])
	slices.Sort(got[:min(2, len(got))])
	if !slices.Equal(got, want) {
		t.Fatalf("backups = %q, want %q", got, want)
	}
	if body, err := os.ReadFile(want[2]); err != nil || !bytes.Equal(body, []byte("old hand")) {
		t.Fatalf("binary copy = %q, %v", body, err)
	}
	for _, p := range want[:2] {
		st, err := state.Open(p, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
	}
}

func TestBackupKeepsTheTwoNewestSets(t *testing.T) {
	root := t.TempDir()
	id, _ := newFleet(t, root, "alpha")
	target := binary(t, "old hand")
	for i := range 3 {
		if _, err := Backup(context.Background(), root, target, entries(t, root), stamp.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, filepath.Join(root, "backups", id)); len(got) != 3 {
		t.Fatalf("Backup pruned before the swap: %q", got)
	}
	if err := Prune(root, entries(t, root)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(root, "backups", id)); !slices.Equal(got, []string{"hand.db.20261002T010204", "hand.db.20261002T010205"}) {
		t.Fatalf("fleet backups = %q", got)
	}
	if got := names(t, filepath.Join(root, "backups")); !slices.Equal(got, []string{id, "hand.20261002T010204", "hand.20261002T010205"}) {
		t.Fatalf("binary backups = %q", got)
	}
}

func TestBackupSkipsAFleetThatIsNotOk(t *testing.T) {
	root := t.TempDir()
	id, home := newFleet(t, root, "alpha")
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	list := entries(t, root)
	if len(list) != 1 || list[0].State != "missing" {
		t.Fatalf("entries = %+v", list)
	}
	got, err := Backup(context.Background(), root, binary(t, "old hand"), list, stamp)
	if err != nil || !slices.Equal(got, []string{filepath.Join(root, "backups", "hand.20261002T010203")}) {
		t.Fatalf("backups = %q, %v", got, err)
	}
	absent(t, filepath.Join(root, "backups", id))
}

func TestBackupMovesPastAnExistingStamp(t *testing.T) {
	root := t.TempDir()
	id, _ := newFleet(t, root, "alpha")
	if err := os.MkdirAll(filepath.Join(root, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backups", "hand.20261002T010203"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Backup(context.Background(), root, binary(t, "old hand"), entries(t, root), stamp)
	want := []string{filepath.Join(root, "backups", id, "hand.db.20261002T010204"), filepath.Join(root, "backups", "hand.20261002T010204")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("backups = %q, %v", got, err)
	}
}

func TestPruneLeavesTheWatcherSnapshots(t *testing.T) {
	root := t.TempDir()
	id, _ := newFleet(t, root, "alpha")
	dir := filepath.Join(root, "backups", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := []string{"hand-20261001.db", "hand-20261002.db", "hand-20261003.db"}
	for _, n := range want {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(root, entries(t, root)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !slices.Equal(got, want) {
		t.Fatalf("Prune left %q, want %q", got, want)
	}
}
