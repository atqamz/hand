package registry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/secondhand"
	"github.com/atqamz/hand/internal/store"
	"pgregory.net/rapid"
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
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_registry (fleet_id, last_known_home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, secondID, home, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_locator (fleet_id, home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, secondID, home, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
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

func TestRegisterReconcilesSupersededSameHomeProjection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fleet registry 測試")
	home := filepath.Join(root, "home with spaces")
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

	registryDB, err := OpenAt(filepath.Join(root, "runtime", "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	if err := registryDB.Register(home, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(home)
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
	if firstID == secondID {
		t.Fatal("recreated Fleet identity unexpectedly stayed the same")
	}

	if err := registryDB.Register(home, secondID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	locators, err := registryDB.Locators()
	if err != nil {
		t.Fatal(err)
	}
	if len(locators) != 1 || locators[0].FleetID != secondID {
		t.Fatalf("locators = %+v, want only recreated Fleet %s", locators, secondID)
	}
	var identities int
	if err := registryDB.sql.QueryRow(`SELECT COUNT(*) FROM fleet_registry`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if identities != 1 {
		t.Fatalf("fleet identities = %d, want one non-orphaned identity", identities)
	}
	if got, err := store.FleetIDReadOnly(home); err != nil || got != secondID {
		t.Fatalf("canonical Fleet identity = %q, %v, want %s unchanged", got, err, secondID)
	}
}

func TestRegisterRollbackRestoresSupersededProjectionOnWriteFailure(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
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
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	if err := registryDB.Register(home, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(home)
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
	if _, err := registryDB.sql.Exec(fmt.Sprintf(`
CREATE TRIGGER fail_register_locator
BEFORE INSERT ON fleet_locator
WHEN NEW.fleet_id = '%s'
BEGIN
	SELECT RAISE(ABORT, 'injected register failure');
END;`, secondID)); err != nil {
		t.Fatal(err)
	}

	if err := registryDB.Register(home, secondID, time.Now().UTC()); err == nil {
		t.Fatal("Register succeeded across injected write failure")
	}
	locators, err := registryDB.Locators()
	if err != nil {
		t.Fatal(err)
	}
	if len(locators) != 1 || locators[0].FleetID != firstID {
		t.Fatalf("locators after rollback = %+v, want original Fleet %s", locators, firstID)
	}
	var identities int
	if err := registryDB.sql.QueryRow(`SELECT COUNT(*) FROM fleet_registry`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if identities != 1 {
		t.Fatalf("fleet identities after rollback = %d, want one", identities)
	}
}

func TestRegisterRetainsSupersededIdentityUsedAtAnotherHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	otherHome := filepath.Join(root, "other")
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
	if err := os.MkdirAll(filepath.Dir(store.Path(otherHome)), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(otherHome), data, 0o600); err != nil {
		t.Fatal(err)
	}

	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	for _, registeredHome := range []string{home, otherHome} {
		if err := registryDB.Register(registeredHome, firstID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(home)
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
	if err := registryDB.Register(home, secondID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, secondID, time.Now().Add(time.Second).UTC()); err != nil {
		t.Fatal(err)
	}

	locators, err := registryDB.Locators()
	if err != nil {
		t.Fatal(err)
	}
	if len(locators) != 2 {
		t.Fatalf("locators = %+v, want retained old-home locator and current locator", locators)
	}
	var oldHome, currentHome bool
	for _, locator := range locators {
		switch {
		case locator.FleetID == firstID && samePath(locator.Home, otherHome):
			oldHome = true
		case locator.FleetID == secondID && samePath(locator.Home, home):
			currentHome = true
		}
	}
	if !oldHome || !currentHome {
		t.Fatalf("locators = %+v, want %s at %s and %s at %s", locators, firstID, otherHome, secondID, home)
	}
	var identities int
	if err := registryDB.sql.QueryRow(`SELECT COUNT(*) FROM fleet_registry`).Scan(&identities); err != nil {
		t.Fatal(err)
	}
	if identities != 2 {
		t.Fatalf("fleet identities = %d, want retained old and current identities", identities)
	}
}

func TestRegisterRefusesUnprovableCanonicalIdentityWithoutDeletingProjection(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
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
	secondHome := filepath.Join(root, "second")
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
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	if err := registryDB.Register(home, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "state")); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, secondID, time.Now().UTC()); err == nil {
		t.Fatal("Register succeeded without a canonical Fleet database")
	}
	locators, err := registryDB.Locators()
	if err != nil {
		t.Fatal(err)
	}
	if len(locators) != 1 || locators[0].FleetID != firstID {
		t.Fatalf("locators after refused repair = %+v, want original Fleet %s", locators, firstID)
	}
}

func TestListScopesAmbiguousClaimsToParticipatingFleets(t *testing.T) {
	root := t.TempDir()
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	ids := make(map[string]string)
	for _, name := range []string{"healthy A", "healthy B", "ambiguous C", "other"} {
		home := filepath.Join(root, name)
		db, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		ids[name], err = db.FleetID()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"healthy A", "healthy B"} {
		if err := registryDB.Register(filepath.Join(root, name), ids[name], time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	ambiguousHome := filepath.Join(root, "ambiguous C")
	if err := registryDB.Register(ambiguousHome, ids["ambiguous C"], time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_registry (fleet_id, last_known_home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, ids["other"], ambiguousHome, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_locator (fleet_id, home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, ids["other"], ambiguousHome, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	fleets, err := registryDB.List("")
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]State, len(fleets))
	for _, fleet := range fleets {
		states[fleet.ID] = fleet.State
	}
	if states[ids["healthy A"]] != StateReady || states[ids["healthy B"]] != StateReady {
		t.Fatalf("healthy Fleet states = %#v, want both ready", states)
	}
	if states[ids["ambiguous C"]] != StateAmbiguous || states[ids["other"]] != StateAmbiguous {
		t.Fatalf("participating Fleet states = %#v, want both ambiguous", states)
	}
}

func TestListAmbiguityPrecedesUnrelatedUnreadableObservation(t *testing.T) {
	root := t.TempDir()
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()

	collidedHome := filepath.Join(root, "collided")
	first, err := store.Open(collidedHome)
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
	if err := registryDB.Register(collidedHome, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	unreadableHome := filepath.Join(root, "unreadable")
	if err := os.MkdirAll(filepath.Dir(store.Path(unreadableHome)), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path(collidedHome))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(unreadableHome), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(unreadableHome, firstID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.Path(unreadableHome)); err != nil {
		t.Fatal(err)
	}

	secondHome := filepath.Join(root, "second")
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
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_registry (fleet_id, last_known_home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, secondID, collidedHome, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := registryDB.sql.Exec(`
INSERT INTO fleet_locator (fleet_id, home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, secondID, collidedHome, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	fleets, err := registryDB.List("")
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]State, len(fleets))
	for _, fleet := range fleets {
		states[fleet.ID] = fleet.State
	}
	if states[firstID] != StateAmbiguous || states[secondID] != StateAmbiguous {
		t.Fatalf("states = %#v, want both participating Fleets ambiguous", states)
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

// Registers home as a fresh Fleet and returns its identity.
func seedFleet(t *testing.T, registryDB *Registry, home string) string {
	t.Helper()
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
	if err := registryDB.Register(home, fleetID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return fleetID
}

func TestMissingFleetsNamesOnlyEntriesClassifiedMissing(t *testing.T) {
	root := t.TempDir()
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()

	readyHome := filepath.Join(root, "ready")
	seedFleet(t, registryDB, readyHome)

	unreadableHome := filepath.Join(root, "unreadable")
	seedFleet(t, registryDB, unreadableHome)
	if err := os.Remove(store.Path(unreadableHome)); err != nil {
		t.Fatal(err)
	}

	missingHome := filepath.Join(root, "missing")
	missingID := seedFleet(t, registryDB, missingHome)
	if err := os.RemoveAll(missingHome); err != nil {
		t.Fatal(err)
	}

	candidates, err := registryDB.MissingFleets(readyHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != missingID || candidates[0].State != StateMissing {
		t.Fatalf("MissingFleets() = %+v, want exactly the missing Fleet %s", candidates, missingID)
	}
}

func TestPruneRemovesOnlyEntriesClassifiedMissingAndNeverTheCurrentHome(t *testing.T) {
	root := t.TempDir()
	registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()

	readyHome := filepath.Join(root, "ready")
	readyID := seedFleet(t, registryDB, readyHome)

	unreadableHome := filepath.Join(root, "unreadable")
	unreadableID := seedFleet(t, registryDB, unreadableHome)
	if err := os.Remove(store.Path(unreadableHome)); err != nil {
		t.Fatal(err)
	}

	missingHome := filepath.Join(root, "missing")
	missingID := seedFleet(t, registryDB, missingHome)
	if err := os.RemoveAll(missingHome); err != nil {
		t.Fatal(err)
	}

	// Prune runs from the ready Fleet's own home. classify() can never mark a Current locator
	// missing, so this also covers "never remove the current home" as far as it is reachable.
	removed, err := registryDB.Prune(readyHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].ID != missingID {
		t.Fatalf("Prune() = %+v, want exactly the missing Fleet %s removed", removed, missingID)
	}

	fleets, err := registryDB.List(readyHome)
	if err != nil {
		t.Fatal(err)
	}
	remaining := make(map[string]State, len(fleets))
	for _, fleet := range fleets {
		remaining[fleet.ID] = fleet.State
	}
	if _, stillThere := remaining[missingID]; stillThere {
		t.Fatalf("fleets after prune = %#v, want %s gone", remaining, missingID)
	}
	if remaining[readyID] != StateReady {
		t.Fatalf("fleets after prune = %#v, want %s still ready", remaining, readyID)
	}
	if remaining[unreadableID] != StateUnreadable {
		t.Fatalf("fleets after prune = %#v, want %s still unreadable", remaining, unreadableID)
	}

	// A second Prune, with nothing left to remove, is a true no-op.
	second, err := registryDB.Prune(readyHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second Prune() = %+v, want nothing left to remove", second)
	}
}

// Draws Unix timestamps clear of time.Time's zero value, so Register's "0001-01-01T00:00:00Z"
// fallback to a real time.Now() never fires and every generated stamp round-trips deterministically
// through RFC3339Nano.
func timestampGen() *rapid.Generator[time.Time] {
	return rapid.Map(rapid.Int64Range(0, 4102444800), func(sec int64) time.Time {
		return time.Unix(sec, 0).UTC()
	})
}

// Returns a validly-formatted fleet id that differs from id at one hex digit, for exercising
// Register's and Check's identity-mismatch path without an invalid-format detour.
func flipFleetID(id string) string {
	b := []byte(id)
	if b[2] == '0' {
		b[2] = '1'
	} else {
		b[2] = '0'
	}
	return string(b)
}

// INV-REG-1: Register is idempotent per (home, fleet id) - repeating it any number of times, with
// any timestamps, yields one registry row and one locator row, not many. The home is built once,
// outside rapid.Check, since no case here ever mutates it and store.Open's migration is not free.
func TestRegisterIsIdempotentAcrossRepeatedCalls(t *testing.T) {
	homeRoot, err := os.MkdirTemp("", "hand-registry-idempotent-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeRoot) })
	home := filepath.Join(homeRoot, "fleet")
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

	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "hand-registry-idempotent-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })

		registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = registryDB.Close() })

		stamps := rapid.SliceOfN(timestampGen(), 1, 8).Draw(t, "stamps")
		for _, stamp := range stamps {
			if err := registryDB.Register(home, fleetID, stamp); err != nil {
				t.Fatalf("Register(%s) = %v", stamp, err)
			}
		}

		var registryRows int
		if err := registryDB.sql.QueryRow(`SELECT COUNT(*) FROM fleet_registry`).Scan(&registryRows); err != nil {
			t.Fatal(err)
		}
		if registryRows != 1 {
			t.Fatalf("fleet_registry rows after %d repeats = %d, want 1", len(stamps), registryRows)
		}
		var locatorRows int
		if err := registryDB.sql.QueryRow(`SELECT COUNT(*) FROM fleet_locator`).Scan(&locatorRows); err != nil {
			t.Fatal(err)
		}
		if locatorRows != 1 {
			t.Fatalf("fleet_locator rows after %d repeats = %d, want 1", len(stamps), locatorRows)
		}

		var firstSeen, lastSeen string
		if err := registryDB.sql.QueryRow(`SELECT first_seen_at, last_seen_at FROM fleet_registry WHERE fleet_id = ?`, fleetID).Scan(&firstSeen, &lastSeen); err != nil {
			t.Fatal(err)
		}
		wantFirst := stamps[0].Format(time.RFC3339Nano)
		wantLast := stamps[len(stamps)-1].Format(time.RFC3339Nano)
		if firstSeen != wantFirst {
			t.Fatalf("first_seen_at = %q, want %q (the first call's stamp, unmoved by every later repeat)", firstSeen, wantFirst)
		}
		if lastSeen != wantLast {
			t.Fatalf("last_seen_at = %q, want %q (the last call's stamp)", lastSeen, wantLast)
		}
	})
}

// INV-REG-2: no registry operation - Register with the right id, Register with a wrong id, List,
// Prune, or Check - ever reissues or rewrites a home's own fleet identity. The home is built once,
// for the same reason as TestRegisterIsIdempotentAcrossRepeatedCalls: no op below mutates it.
func TestNoRegistryOperationRewritesAHomesFleetIdentity(t *testing.T) {
	homeRoot, err := os.MkdirTemp("", "hand-registry-identity-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeRoot) })
	home := filepath.Join(homeRoot, "fleet")
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	canonicalID, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	wrongID := flipFleetID(canonicalID)

	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "hand-registry-identity-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })

		registryDB, err := OpenAt(filepath.Join(root, "registry.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = registryDB.Close() })

		ops := rapid.SliceOfN(rapid.SampledFrom([]string{
			"register-correct", "register-wrong", "list", "prune", "check-correct", "check-wrong",
		}), 1, 8).Draw(t, "ops")

		for i, op := range ops {
			stamp := timestampGen().Draw(t, fmt.Sprintf("stamp-%d", i))
			switch op {
			case "register-correct":
				_ = registryDB.Register(home, canonicalID, stamp)
			case "register-wrong":
				_ = registryDB.Register(home, wrongID, stamp)
			case "list":
				_, _ = registryDB.List(home)
			case "prune":
				_, _ = registryDB.Prune(home)
			case "check-correct":
				_ = registryDB.Check(home, canonicalID)
			case "check-wrong":
				_ = registryDB.Check(home, wrongID)
			}

			got, err := store.FleetIDReadOnly(home)
			if err != nil {
				t.Fatalf("FleetIDReadOnly after %q = %v", op, err)
			}
			if got != canonicalID {
				t.Fatalf("FleetIDReadOnly after %q = %q, want unchanged %q", op, got, canonicalID)
			}
		}
	})
}

// A pre-built fleet home whose canonical identity and state/hand.db bytes are fixed, so a rapid
// property can reset it to that exact state instead of paying store.Open's schema migration again on
// every one of the ~100 generated cases.
type homeFixture struct {
	dir     string
	fleetID string
	dbBytes []byte
}

func buildHomeFixtures(t *testing.T, root string, n int) []homeFixture {
	t.Helper()
	fixtures := make([]homeFixture, n)
	for i := range fixtures {
		home := filepath.Join(root, fmt.Sprintf("home-%d", i))
		db, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		id, err := db.FleetID()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(store.Path(home))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[i] = homeFixture{dir: home, fleetID: id, dbBytes: data}
	}
	return fixtures
}

// Restores f's directory to holding exactly its pristine state/hand.db, regardless of what an
// earlier case did to it - removed the whole directory, or just the database file.
func (f homeFixture) reset(t *rapid.T) {
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Dir(f.dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(f.dir), f.dbBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

// INV-REG-3: classification is a function of (stored rows, observed filesystem), and reading it
// mutates nothing - checked against the registry's raw bytes, not just the returned value. Homes
// reset per case rather than rebuild, since this is the only property of the four that mutates one.
func TestListIsPureAndReadingMutatesNothing(t *testing.T) {
	fixtureRoot, err := os.MkdirTemp("", "hand-registry-pure-homes-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureRoot) })
	fixtures := buildHomeFixtures(t, fixtureRoot, 5)

	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "hand-registry-pure-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })

		registryPath := filepath.Join(root, "registry.db")
		registryDB, err := OpenAt(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = registryDB.Close() })

		n := rapid.IntRange(1, len(fixtures)).Draw(t, "homes")
		var current string
		for i := 0; i < n; i++ {
			fixture := fixtures[i]
			fixture.reset(t)
			stamp := timestampGen().Draw(t, fmt.Sprintf("stamp-%d", i))
			if err := registryDB.Register(fixture.dir, fixture.fleetID, stamp); err != nil {
				t.Fatal(err)
			}
			switch rapid.SampledFrom([]string{"ready", "missing", "unreadable"}).Draw(t, fmt.Sprintf("kind-%d", i)) {
			case "missing":
				if err := os.RemoveAll(fixture.dir); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Remove(store.Path(fixture.dir)); err != nil {
					t.Fatal(err)
				}
			}
			if i == 0 {
				current = fixture.dir
			}
		}

		before, err := os.ReadFile(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		first, err := registryDB.List(current)
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("registry.db changed by a read-only List(): %d bytes before, %d bytes after", len(before), len(after))
		}

		second, err := registryDB.List(current)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("List() twice over the same unchanged (stored rows, filesystem) = %+v then %+v, want identical", first, second)
		}
	})
}

// INV-REG-4: losing or deleting the registry changes no fleet identity, and re-registering restores
// exactly what each home's own state/hand.db carries. Homes are built once and never mutated here
// (unlike TestListIsPureAndReadingMutatesNothing), so the fixed pool needs no reset between cases.
func TestRegistryLossLosesNoIdentityAndRegisterRecoversIt(t *testing.T) {
	fixtureRoot, err := os.MkdirTemp("", "hand-registry-recovery-homes-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureRoot) })
	fixtures := buildHomeFixtures(t, fixtureRoot, 4)

	rapid.Check(t, func(t *rapid.T) {
		root, err := os.MkdirTemp("", "hand-registry-recovery-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })

		registryPath := filepath.Join(root, "registry.db")
		registryDB, err := OpenAt(registryPath)
		if err != nil {
			t.Fatal(err)
		}

		n := rapid.IntRange(1, len(fixtures)).Draw(t, "homes")
		homes := make([]string, n)
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			stamp := timestampGen().Draw(t, fmt.Sprintf("stamp-%d", i))
			if err := registryDB.Register(fixtures[i].dir, fixtures[i].fleetID, stamp); err != nil {
				t.Fatal(err)
			}
			homes[i] = fixtures[i].dir
			ids[i] = fixtures[i].fleetID
		}
		if err := registryDB.Close(); err != nil {
			t.Fatal(err)
		}

		if err := os.Remove(registryPath); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
			t.Fatalf("registry.db still present after removal")
		}

		for i, home := range homes {
			got, err := store.FleetIDReadOnly(home)
			if err != nil {
				t.Fatalf("FleetIDReadOnly(%s) after registry loss = %v", home, err)
			}
			if got != ids[i] {
				t.Fatalf("home %s identity = %q after registry loss, want unchanged %q", home, got, ids[i])
			}
		}

		recovered, err := OpenAt(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = recovered.Close() })
		for i, home := range homes {
			stamp := timestampGen().Draw(t, fmt.Sprintf("recovery-stamp-%d", i))
			if err := recovered.Register(home, ids[i], stamp); err != nil {
				t.Fatalf("Register(%s) after loss = %v", home, err)
			}
		}

		fleets, err := recovered.List("")
		if err != nil {
			t.Fatal(err)
		}
		if len(fleets) != n {
			t.Fatalf("List() after recovery = %+v, want %d fleets", fleets, n)
		}
		recoveredIDs := make(map[string]bool, n)
		for _, fleet := range fleets {
			if fleet.State != StateReady {
				t.Fatalf("fleet %+v after recovery, want ready", fleet)
			}
			recoveredIDs[fleet.ID] = true
		}
		for _, id := range ids {
			if !recoveredIDs[id] {
				t.Fatalf("recovered fleets = %+v, want to include %s", fleets, id)
			}
		}
	})
}
