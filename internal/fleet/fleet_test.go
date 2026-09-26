package fleet_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func home(t *testing.T, dir, name string) state.Fleet {
	t.Helper()
	st, err := state.Open(filepath.Join(dir, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f, err := st.CreateFleet(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestRootDefaultsToDotSecondhandUnderHome(t *testing.T) {
	h := t.TempDir()
	root, err := fleet.Root(env(map[string]string{"HOME": h}))
	if err != nil || root != filepath.Join(h, ".secondhand") {
		t.Fatalf("root = %q, %v", root, err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("root not created: %v", err)
	}
	custom := filepath.Join(t.TempDir(), "infra")
	if root, err := fleet.Root(env(map[string]string{"HOME": h, "SECONDHAND_HOME": custom})); err != nil || root != custom {
		t.Fatalf("custom root = %q, %v", root, err)
	}
	if _, err := fleet.Root(env(nil)); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("no HOME = %v, want ErrInvalid", err)
	}
}

func TestCheckRegistersAHomeAndAcceptsItAgain(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	for range 2 {
		if err := fleet.Check(root, f.ID, dir); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != dir {
		t.Fatalf("home = %q, %v", got, err)
	}
	if _, err := fleet.Home(root, "f000000000000"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("unknown id = %v, want ErrInvalid", err)
	}
}

func TestACopyIsRefusedAndAMoveNeedsRegister(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	if err := fleet.Check(root, f.ID, dir); err != nil {
		t.Fatal(err)
	}
	cp := t.TempDir()
	db, err := os.ReadFile(filepath.Join(dir, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cp, "hand.db"), db, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Check(root, f.ID, cp); !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), "also at "+dir) {
		t.Fatalf("copy check = %v", err)
	}
	if _, err := fleet.Register(root, f.ID, cp); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("copy register = %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Check(root, f.ID, cp); !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), "moved here from "+dir) {
		t.Fatalf("moved check = %v", err)
	}
	if prev, err := fleet.Register(root, f.ID, cp); err != nil || prev != dir {
		t.Fatalf("register = %q, %v", prev, err)
	}
	if err := fleet.Check(root, f.ID, cp); err != nil {
		t.Fatalf("check after register = %v", err)
	}
}

func TestAMovedHomeWithASymlinkBackIsTheSameFleet(t *testing.T) {
	root, parent := t.TempDir(), t.TempDir()
	old := filepath.Join(parent, "old")
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	f := home(t, old, "alpha")
	if err := fleet.Check(root, f.ID, old); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, old); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Check(root, f.ID, moved); err != nil {
		t.Fatalf("check through the old symlink = %v", err)
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != moved {
		t.Fatalf("link = %q, %v; want it re-pointed at %s", got, err, moved)
	}
	if prev, err := fleet.Register(root, f.ID, moved); err != nil || prev != "" {
		t.Fatalf("register = %q, %v", prev, err)
	}
}

func TestConcurrentFirstChecksAllSucceed(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- fleet.Check(root, f.ID, dir) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if des, err := os.ReadDir(filepath.Join(root, "fleets")); err != nil || len(des) != 1 {
		t.Fatalf("fleets dir = %v, %v; want one link and no temp files", des, err)
	}
}

func TestListShowsEachFleetAndItsState(t *testing.T) {
	root := t.TempDir()
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	fa, fb, fc := home(t, a, "alpha"), home(t, b, "beta"), home(t, c, "gamma")
	for _, h := range []struct {
		id, dir string
	}{{fa.ID, a}, {fb.ID, b}, {fc.ID, c}} {
		if err := fleet.Check(root, h.id, h.dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c, "hand.db")); err != nil {
		t.Fatal(err)
	}
	home(t, c, "delta")
	got, err := fleet.List(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]fleet.Entry{
		fa.ID: {ID: fa.ID, Name: "alpha", Home: a, State: "ok"},
		fb.ID: {ID: fb.ID, Home: b, State: "missing"},
		fc.ID: {ID: fc.ID, Home: c, State: "moved"},
	}
	if len(got) != len(want) {
		t.Fatalf("list = %+v", got)
	}
	for _, e := range got {
		if want[e.ID] != e {
			t.Fatalf("entry %+v, want %+v", e, want[e.ID])
		}
	}
}
