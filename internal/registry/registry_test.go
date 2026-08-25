package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/secondhand"
	"github.com/atqamz/hand/internal/store"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "hand-registry-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SECONDHAND_HOME", filepath.Join(root, "Secondhand 測試")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestPathUsesConfiguredSecondhandHome(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "Secondhand 測試")
	t.Setenv("SECONDHAND_HOME", configured)
	operatorHome := filepath.Join(t.TempDir(), "operator")
	t.Setenv("HOME", operatorHome)
	t.Setenv("USERPROFILE", operatorHome)
	t.Setenv("HAND_HOME", filepath.Join(t.TempDir(), "fleet"))

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want, err := secondhand.Home()
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(want, "registry.db")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestOpenDoesNotTouchProductionRegistryWhenOverrideIsSet(t *testing.T) {
	operatorHome := filepath.Join(t.TempDir(), "operator")
	sentinelPath := filepath.Join(operatorHome, ".secondhand", "registry.db")
	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("operator registry sentinel")
	if err := os.WriteFile(sentinelPath, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", operatorHome)
	t.Setenv("USERPROFILE", operatorHome)
	t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "Secondhand 測試"))

	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(sentinel) {
		t.Fatalf("production registry changed from %q to %q", sentinel, got)
	}
}

func TestRegisterAndListReadyFleet(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, ".secondhand", "registry.db")
	home := filepath.Join(root, "fleet")
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

	registry, err := OpenAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registry.Close() }()
	if err := registry.Register(home, fleetID, time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	fleets, err := registry.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(fleets) != 1 {
		t.Fatalf("fleets = %+v, want one fleet", fleets)
	}
	if fleets[0].ID != fleetID || fleets[0].State != StateReady || !fleets[0].Current {
		t.Fatalf("fleet = %+v, want ready current %s", fleets[0], fleetID)
	}
	canonicalHome, err := canonicalPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(fleets[0].Locations) != 1 || fleets[0].Locations[0] != canonicalHome {
		t.Fatalf("locations = %+v, want %q", fleets[0].Locations, canonicalHome)
	}
}

func TestListReportsCopiedFleetIdentityAsDuplicate(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, ".secondhand", "registry.db")
	firstHome := filepath.Join(root, "first")
	secondHome := filepath.Join(root, "second")

	db, err := store.Open(firstHome)
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
	if err := os.MkdirAll(filepath.Dir(store.Path(secondHome)), 0o755); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(store.Path(firstHome))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(secondHome), database, 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := OpenAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registry.Close() }()
	for _, home := range []string{firstHome, secondHome} {
		if err := registry.Register(home, fleetID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	fleets, err := registry.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(fleets) != 1 || fleets[0].State != StateDuplicate || len(fleets[0].Locations) != 2 {
		t.Fatalf("fleets = %+v, want one duplicate with two locations", fleets)
	}
	var duplicate *DuplicateError
	if err := registry.Check(secondHome, fleetID); !errors.As(err, &duplicate) {
		t.Fatalf("Check error = %v, want DuplicateError", err)
	}
	if duplicate.Current == "" || len(duplicate.Other) != 1 {
		t.Fatalf("duplicate = %+v, want current and one other home", duplicate)
	}
}

func TestReadOnlyMissingRegistryDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".secondhand", "registry.db")
	if _, err := OpenReadOnlyAt(path); !errors.Is(err, ErrRegistryMissing) {
		t.Fatalf("OpenReadOnlyAt error = %v, want ErrRegistryMissing", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("registry stat error = %v, want no file", err)
	}
}

func TestListReportsAmbiguousHomeClaims(t *testing.T) {
	root := t.TempDir()
	registryPath := filepath.Join(root, ".secondhand", "registry.db")
	home := filepath.Join(root, "fleet")
	first, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := first.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	secondHome := filepath.Join(root, "other")
	second, err := store.Open(secondHome)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := second.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	registryDB, err := OpenAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	if err := registryDB.Register(home, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, secondID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	fleets, err := registryDB.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(fleets) != 2 || fleets[0].State != StateAmbiguous || fleets[1].State != StateAmbiguous {
		t.Fatalf("fleets = %+v, want both identities ambiguous", fleets)
	}
}

func TestPreflightRefusesARegisteredDuplicateBeforeRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	firstHome := filepath.Join(root, "first")
	secondHome := filepath.Join(root, "second")
	first, err := store.Open(firstHome)
	if err != nil {
		t.Fatal(err)
	}
	fleetID, err := first.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path(secondHome)), 0o755); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(store.Path(firstHome))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(secondHome), database, 0o600); err != nil {
		t.Fatal(err)
	}
	registryDB, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(firstHome, fleetID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(secondHome, fleetID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}
	var duplicate *DuplicateError
	if _, err := Preflight(secondHome, false); !errors.As(err, &duplicate) {
		t.Fatalf("Preflight() = %v, want DuplicateError", err)
	}
}
