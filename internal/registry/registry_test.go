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
