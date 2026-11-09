package fleet_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	if des, err := os.ReadDir(filepath.Join(root, "index")); err != nil || len(des) != 1 {
		t.Fatalf("index dir = %v, %v; want one bookmark and no temp files", des, err)
	}
}

func TestTwoCopiesRegisteringAtOnceCannotBothWin(t *testing.T) {
	for range 20 {
		root, a, b := t.TempDir(), t.TempDir(), t.TempDir()
		f := home(t, a, "alpha")
		db, err := os.ReadFile(filepath.Join(a, "hand.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b, "hand.db"), db, 0o600); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		won := make([]bool, 2)
		for i, dir := range []string{a, b} {
			wg.Go(func() { won[i] = fleet.Check(root, f.ID, dir) == nil })
		}
		wg.Wait()
		if won[0] && won[1] {
			t.Fatal("both copies claimed the same fleet id")
		}
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

func TestPointerFileClaimExclusive(t *testing.T) {
	root, dir, other := t.TempDir(), t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	if err := fleet.Claim(root, f.ID, dir); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Claim(root, f.ID, other); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second claim = %v, want ErrExist", err)
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != dir {
		t.Fatalf("home = %q, %v", got, err)
	}
}

func TestAnEmptyPointerFileIsInvalid(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index", "f000000000000"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Home(root, "f000000000000"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("home = %v, want ErrInvalid", err)
	}
	if _, err := fleet.Register(root, "f000000000000", root); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("register = %v, want ErrInvalid", err)
	}
}

func TestRegisterRewritesAPointerThatNamesTheSameFolderByAnotherPath(t *testing.T) {
	root, parent := t.TempDir(), t.TempDir()
	real, alias := filepath.Join(parent, "real"), filepath.Join(parent, "alias")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	f := home(t, real, "alpha")
	if err := fleet.Claim(root, f.ID, alias); err != nil {
		t.Fatal(err)
	}
	if prev, err := fleet.Register(root, f.ID, real); err != nil || prev != "" {
		t.Fatalf("register = %q, %v", prev, err)
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != real {
		t.Fatalf("link = %q, %v; want it rewritten to %s", got, err, real)
	}
}

func TestALeftoverSymlinkNamesItsPathAndTheFixAndSparesTheOtherFleets(t *testing.T) {
	root, dir, old := t.TempDir(), t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	if err := fleet.Check(root, f.ID, dir); err != nil {
		t.Fatal(err)
	}
	stale := "f000000000001"
	path := filepath.Join(root, "index", stale)
	if err := os.Symlink(old, path); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	_, err := fleet.Home(root, stale)
	if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "delete it and run `hand init` in the fleet's folder") {
		t.Fatalf("home = %v", err)
	}
	got, err := fleet.List(root)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	for _, e := range got {
		switch e.ID {
		case f.ID:
			if e.State != "ok" {
				t.Fatalf("entry %+v", e)
			}
		case stale:
			if !strings.HasPrefix(e.State, "unreadable: ") || !strings.Contains(e.State, path) {
				t.Fatalf("entry %+v", e)
			}
		default:
			t.Fatalf("entry %+v", e)
		}
	}
}

func TestADanglingSymlinkIsInvalidNotMissing(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	if err := os.MkdirAll(filepath.Join(root, "index"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index", f.ID)
	if err := os.Symlink(filepath.Join(root, "gone"), path); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for name, call := range map[string]func() error{
		"home":     func() error { _, err := fleet.Home(root, f.ID); return err },
		"register": func() error { _, err := fleet.Register(root, f.ID, dir); return err },
		"check":    func() error { return fleet.Check(root, f.ID, dir) },
	} {
		if err := call(); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), path) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

func TestAPlainFolderInPlaceOfTheLinkIsNotCalledASymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "index", "f000000000002")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Home(root, "f000000000002"); err == nil || strings.Contains(err.Error(), "symlink") {
		t.Fatalf("home = %v", err)
	}
}

func TestCheckRecordsTheFleetHome(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	if err := fleet.Check(root, f.ID, dir); err != nil {
		t.Fatal(err)
	}
	rec, err := fleet.ReadHomes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Current != dir || !slices.Equal(rec.Homes, []string{dir}) {
		t.Fatalf("record = %+v, want current %s", rec, dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, fleet.HomeFile)); err != nil || !strings.Contains(string(b), `"current":`) {
		t.Fatalf("record file = %q, %v", b, err)
	}
}

func TestAMoveKeepsEveryEarlierHomeInTheRecord(t *testing.T) {
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
	prev, err := fleet.Register(root, f.ID, moved)
	if err != nil || prev != old {
		t.Fatalf("register = %q, %v; want it to report %s", prev, err, old)
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != moved {
		t.Fatalf("bookmark = %q, %v", got, err)
	}
	rec, err := fleet.ReadHomes(moved)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Current != moved || !slices.Equal(rec.Homes, []string{old, moved}) {
		t.Fatalf("record = %+v, want both homes and current %s", rec, moved)
	}
}

func TestLegacyFleetsDirIsMigratedIntoTheIndex(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	f := home(t, dir, "alpha")
	legacy := filepath.Join(root, "fleets")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, f.ID), []byte(dir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := fleet.Home(root, f.ID); err != nil || got != dir {
		t.Fatalf("home = %q, %v", got, err)
	}
	if des, err := os.ReadDir(fleet.IndexDir(root)); err != nil || len(des) != 1 || des[0].Name() != f.ID {
		t.Fatalf("index = %v, %v", des, err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy dir still there: %v", err)
	}
}
