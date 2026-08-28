package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
)

func TestFleetListsKnownHomesOutsideFleetContext(t *testing.T) {
	userHome := t.TempDir()
	setTestUserHome(t, userHome)
	t.Setenv("HAND_HOME", "")
	t.Chdir(t.TempDir())
	firstHome := filepath.Join(t.TempDir(), "first")
	secondHome := filepath.Join(t.TempDir(), "second")
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	for _, home := range []string{firstHome, secondHome} {
		db, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		fleetID, err := db.FleetID()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := registryDB.Register(home, fleetID, testNow()); err != nil {
			t.Fatal(err)
		}
	}

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fleets[2]{id,home,state,current,locations}:", "ready,false", "current_home: none"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("fleet output = %q, want %q", out.String(), want)
		}
	}
}

func TestFleetListsHomesWhenExplicitContextIsInvalid(t *testing.T) {
	userHome := t.TempDir()
	setTestUserHome(t, userHome)
	badHome := filepath.Join(t.TempDir(), "not-a-fleet")
	t.Setenv("HAND_HOME", badHome)
	t.Chdir(t.TempDir())
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	home := filepath.Join(t.TempDir(), "fleet")
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	fleetID, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, fleetID, testNow()); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "current_error:") || !strings.Contains(out.String(), "HAND_HOME") || !strings.Contains(out.String(), "fleets[1]") {
		t.Fatalf("fleet output = %q, want context error and listing", out.String())
	}
}

func testNow() (now time.Time) {
	return time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
}

// Registers a ready, an existing-but-unreadable, and a missing Fleet in the registry rooted at
// userHome, and returns their ids in that order.
func seedFleetHomes(t *testing.T, userHome string) (readyID, unreadableID, missingID string) {
	t.Helper()
	setTestUserHome(t, userHome)
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()

	register := func(home string) string {
		db, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		fleetID, err := db.FleetID()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := registryDB.Register(home, fleetID, testNow()); err != nil {
			t.Fatal(err)
		}
		return fleetID
	}

	readyHome := filepath.Join(userHome, "ready")
	readyID = register(readyHome)

	unreadableHome := filepath.Join(userHome, "unreadable")
	unreadableID = register(unreadableHome)
	if err := os.Remove(store.Path(unreadableHome)); err != nil {
		t.Fatal(err)
	}

	missingHome := filepath.Join(userHome, "missing")
	missingID = register(missingHome)
	if err := os.RemoveAll(missingHome); err != nil {
		t.Fatal(err)
	}
	return readyID, unreadableID, missingID
}

func TestFleetPruneReportsWithoutMutatingByDefault(t *testing.T) {
	userHome := t.TempDir()
	_, _, missingID := seedFleetHomes(t, userHome)
	t.Setenv("HAND_HOME", "")
	t.Chdir(t.TempDir())

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet", "prune"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "applied: false") || !strings.Contains(out.String(), "candidates[1]") || !strings.Contains(out.String(), missingID) {
		t.Fatalf("fleet prune output = %q, want one unapplied missing candidate %s", out.String(), missingID)
	}

	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	fleets, err := registryDB.List("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fleet := range fleets {
		if fleet.ID == missingID {
			found = true
		}
	}
	if !found {
		t.Fatalf("fleets after dry-run prune = %+v, want %s still registered", fleets, missingID)
	}
}

func TestFleetPruneApplyRemovesOnlyMissingEntries(t *testing.T) {
	userHome := t.TempDir()
	readyID, unreadableID, missingID := seedFleetHomes(t, userHome)
	t.Setenv("HAND_HOME", "")
	t.Chdir(t.TempDir())

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet", "prune", "--apply"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "applied: true") || !strings.Contains(out.String(), "removed[1]") || !strings.Contains(out.String(), missingID) {
		t.Fatalf("fleet prune --apply output = %q, want exactly %s removed", out.String(), missingID)
	}

	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	fleets, err := registryDB.List("")
	if err != nil {
		t.Fatal(err)
	}
	remaining := make(map[string]bool, len(fleets))
	for _, fleet := range fleets {
		remaining[fleet.ID] = true
	}
	if remaining[missingID] {
		t.Fatalf("fleets after prune --apply = %+v, want %s gone", fleets, missingID)
	}
	if !remaining[readyID] || !remaining[unreadableID] {
		t.Fatalf("fleets after prune --apply = %+v, want %s and %s to survive", fleets, readyID, unreadableID)
	}
}
