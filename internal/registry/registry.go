// Package registry stores the per-user, non-authoritative index of Fleet homes.
package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/secondhand"
	"github.com/atqamz/hand/internal/store"
	_ "modernc.org/sqlite"
)

var ErrRegistryMissing = errors.New("fleet registry missing")

type State string

const (
	StateReady            State = "ready"
	StateMissing          State = "missing"
	StateIdentityMismatch State = "identity-mismatch"
	StateDuplicate        State = "duplicate"
	StateAmbiguous        State = "ambiguous"
	StateUnreadable       State = "unreadable"
)

type Fleet struct {
	ID        string
	Home      string
	Locations []string
	State     State
	Current   bool
	Reason    string
}

type Locator struct {
	FleetID     string
	Home        string
	FirstSeenAt string
	LastSeenAt  string
}

type Registry struct {
	path string
	sql  *sql.DB
}

func Preflight(home string, readOnly bool) ([]string, error) {
	var fleetID string
	if readOnly {
		var err error
		fleetID, err = store.FleetIDReadOnly(home)
		if err != nil {
			if errors.Is(err, store.ErrFleetIdentityMissing) {
				return nil, nil
			}
			return nil, err
		}
	} else {
		db, err := store.Open(home)
		if err != nil {
			return nil, err
		}
		fleetID, err = db.FleetID()
		_ = db.Close()
		if err != nil {
			return nil, err
		}
	}
	path, err := Path()
	if err != nil {
		return []string{"warning: Fleet registry unavailable: " + err.Error()}, nil
	}
	registry, err := OpenReadOnlyAt(path)
	if errors.Is(err, ErrRegistryMissing) {
		return nil, nil
	}
	if err != nil {
		return []string{fmt.Sprintf("warning: Fleet registry %s unavailable: %v", path, err)}, nil
	}
	defer func() { _ = registry.Close() }()
	if err := registry.Check(home, fleetID); err != nil {
		return nil, err
	}
	return nil, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS fleet_registry (
	fleet_id TEXT PRIMARY KEY,
	last_known_home TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS fleet_locator (
	fleet_id TEXT NOT NULL REFERENCES fleet_registry(fleet_id),
	home TEXT NOT NULL,
	first_seen_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	PRIMARY KEY (fleet_id, home)
);
CREATE INDEX IF NOT EXISTS fleet_locator_by_id ON fleet_locator(fleet_id, home);
`

func Path() (string, error) {
	home, err := secondhand.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "registry.db"), nil
}

func Open() (*Registry, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return OpenAt(path)
}

func OpenAt(path string) (*Registry, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve registry path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create registry directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("protect registry directory: %w", err)
	}
	db, err := open(path, false)
	if err != nil {
		return nil, err
	}
	registry := &Registry{path: path, sql: db}
	if err := registry.withWriteLock(func() error {
		if _, err := db.Exec(schema); err != nil {
			return fmt.Errorf("create registry schema: %w", err)
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil && runtime.GOOS != "windows" {
		_ = db.Close()
		return nil, fmt.Errorf("protect registry database: %w", err)
	}
	return registry, nil
}

func OpenReadOnlyAt(path string) (*Registry, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve registry path: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, ErrRegistryMissing
	} else if err != nil {
		return nil, fmt.Errorf("inspect registry: %w", err)
	}
	db, err := open(path, true)
	if err != nil {
		return nil, err
	}
	return &Registry{path: path, sql: db}, nil
}

func open(path string, readOnly bool) (*sql.DB, error) {
	query := "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	if readOnly {
		query = "?mode=ro&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=query_only(1)"
	}
	uriPath := filepath.ToSlash(path)
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: uriPath}).EscapedPath()+query)
	if err != nil {
		return nil, fmt.Errorf("open registry %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func (r *Registry) Close() error {
	return r.sql.Close()
}

func (r *Registry) Register(home, fleetID string, now time.Time) error {
	if err := store.ValidateFleetID(fleetID); err != nil {
		return err
	}
	home, err := canonicalPath(home)
	if err != nil {
		return fmt.Errorf("canonicalize Fleet home: %w", err)
	}
	canonicalID, err := store.FleetIDReadOnly(home)
	if err != nil {
		return fmt.Errorf("read canonical Fleet identity: %w", err)
	}
	if canonicalID != fleetID {
		return fmt.Errorf("canonical Fleet identity is %s, not %s", canonicalID, fleetID)
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if stamp == "0001-01-01T00:00:00Z" {
		stamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return r.withWriteLock(func() error {
		tx, err := r.sql.Begin()
		if err != nil {
			return fmt.Errorf("begin registry registration: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		stale, err := staleLocators(tx, home, fleetID)
		if err != nil {
			return err
		}
		for _, locator := range stale {
			if _, err := tx.Exec(`DELETE FROM fleet_locator WHERE fleet_id = ? AND home = ?`, locator.FleetID, locator.Home); err != nil {
				return fmt.Errorf("retire superseded Fleet locator: %w", err)
			}
		}
		if _, err := tx.Exec(`
INSERT INTO fleet_registry (fleet_id, last_known_home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(fleet_id) DO UPDATE SET last_known_home = excluded.last_known_home, last_seen_at = excluded.last_seen_at`, fleetID, home, stamp, stamp); err != nil {
			return fmt.Errorf("write Fleet registry row: %w", err)
		}
		if _, err := tx.Exec(`
INSERT INTO fleet_locator (fleet_id, home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(fleet_id, home) DO UPDATE SET last_seen_at = excluded.last_seen_at`, fleetID, home, stamp, stamp); err != nil {
			return fmt.Errorf("write Fleet locator: %w", err)
		}
		for _, fleetID := range staleIDs(stale) {
			if _, err := tx.Exec(`
DELETE FROM fleet_registry
WHERE fleet_id = ?
  AND NOT EXISTS (SELECT 1 FROM fleet_locator WHERE fleet_id = ?)`, fleetID, fleetID); err != nil {
				return fmt.Errorf("remove orphaned Fleet registry row: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit Fleet registration: %w", err)
		}
		return nil
	})
}

func staleLocators(tx *sql.Tx, selectedHome, fleetID string) ([]Locator, error) {
	selectedKey, err := pathKey(selectedHome)
	if err != nil {
		return nil, fmt.Errorf("resolve selected Fleet home for registry repair: %w", err)
	}
	rows, err := tx.Query(`SELECT fleet_id, home, first_seen_at, last_seen_at FROM fleet_locator ORDER BY fleet_id, home`)
	if err != nil {
		return nil, fmt.Errorf("inspect Fleet locators for registry repair: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var stale []Locator
	for rows.Next() {
		var locator Locator
		if err := rows.Scan(&locator.FleetID, &locator.Home, &locator.FirstSeenAt, &locator.LastSeenAt); err != nil {
			return nil, fmt.Errorf("read Fleet locator for registry repair: %w", err)
		}
		locatorKey, err := pathKey(locator.Home)
		if err != nil {
			return nil, fmt.Errorf("resolve registered Fleet home %q for registry repair: %w", locator.Home, err)
		}
		if locatorKey != selectedKey || locator.FleetID == fleetID {
			continue
		}
		var present int
		if err := tx.QueryRow(`SELECT 1 FROM fleet_registry WHERE fleet_id = ?`, locator.FleetID).Scan(&present); err != nil {
			return nil, fmt.Errorf("validate superseded Fleet %s registry row: %w", locator.FleetID, err)
		}
		stale = append(stale, locator)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect Fleet locators for registry repair: %w", err)
	}
	return stale, nil
}

func staleIDs(locators []Locator) []string {
	ids := make([]string, 0, len(locators))
	seen := make(map[string]bool, len(locators))
	for _, locator := range locators {
		if !seen[locator.FleetID] {
			seen[locator.FleetID] = true
			ids = append(ids, locator.FleetID)
		}
	}
	return ids
}

func (r *Registry) Locators() ([]Locator, error) {
	rows, err := r.sql.Query(`SELECT fleet_id, home, first_seen_at, last_seen_at FROM fleet_locator ORDER BY fleet_id, home`)
	if err != nil {
		return nil, fmt.Errorf("list Fleet locators: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var locators []Locator
	for rows.Next() {
		var locator Locator
		if err := rows.Scan(&locator.FleetID, &locator.Home, &locator.FirstSeenAt, &locator.LastSeenAt); err != nil {
			return nil, fmt.Errorf("read Fleet locator: %w", err)
		}
		locators = append(locators, locator)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Fleet locators: %w", err)
	}
	return locators, nil
}

func (r *Registry) List(currentHome string) ([]Fleet, error) {
	locators, err := r.Locators()
	if err != nil {
		return nil, err
	}
	current, _ := canonicalPath(currentHome)
	byID := make(map[string][]Locator)
	claims := make(map[string]map[string]bool)
	for _, locator := range locators {
		byID[locator.FleetID] = append(byID[locator.FleetID], locator)
		key, _ := pathKey(locator.Home)
		if claims[key] == nil {
			claims[key] = make(map[string]bool)
		}
		claims[key][locator.FleetID] = true
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	result := make([]Fleet, 0, len(ids))
	for _, id := range ids {
		fleet := Fleet{ID: id}
		observations := make([]observation, 0, len(byID[id]))
		for _, locator := range byID[id] {
			fleet.Locations = append(fleet.Locations, locator.Home)
			observations = append(observations, observe(locator))
		}
		sort.Strings(fleet.Locations)
		fleet.Home = lastKnownHome(r.sql, id, fleet.Locations)
		fleet.State, fleet.Reason = classify(observations, claims)
		for _, observed := range observations {
			if observed.valid && samePath(observed.locator.Home, current) {
				fleet.Current = true
				break
			}
		}
		result = append(result, fleet)
	}
	return result, nil
}

type observation struct {
	locator Locator
	valid   bool
	state   State
	reason  string
}

func observe(locator Locator) observation {
	info, err := os.Stat(locator.Home)
	if os.IsNotExist(err) {
		return observation{locator: locator, state: StateMissing}
	}
	if err != nil {
		return observation{locator: locator, state: StateUnreadable, reason: err.Error()}
	}
	if !info.IsDir() {
		return observation{locator: locator, state: StateUnreadable, reason: "Fleet home is not a directory"}
	}
	if _, err := os.Stat(store.Path(locator.Home)); err != nil {
		if os.IsNotExist(err) {
			return observation{locator: locator, state: StateUnreadable, reason: "state/hand.db is missing"}
		}
		return observation{locator: locator, state: StateUnreadable, reason: err.Error()}
	}
	db, err := store.OpenReadOnly(locator.Home)
	if err != nil {
		return observation{locator: locator, state: StateUnreadable, reason: err.Error()}
	}
	defer func() { _ = db.Close() }()
	observedID, err := db.FleetID()
	if err != nil {
		return observation{locator: locator, state: StateIdentityMismatch, reason: err.Error()}
	}
	if observedID != locator.FleetID {
		return observation{locator: locator, state: StateIdentityMismatch, reason: fmt.Sprintf("authoritative identity is %s", observedID)}
	}
	return observation{locator: locator, valid: true, state: StateReady}
}

func classify(observations []observation, claims map[string]map[string]bool) (State, string) {
	valid := 0
	missing := 0
	var mismatch, unreadable []string
	for _, observed := range observations {
		switch {
		case observed.valid:
			valid++
		case observed.state == StateMissing:
			missing++
		case observed.state == StateIdentityMismatch:
			mismatch = append(mismatch, observed.locator.Home+": "+observed.reason)
		case observed.state == StateUnreadable:
			unreadable = append(unreadable, observed.locator.Home+": "+observed.reason)
		}
	}
	if valid > 1 {
		return StateDuplicate, "the same Fleet identity is valid at multiple homes"
	}
	for _, ids := range claims {
		if len(ids) > 1 {
			return StateAmbiguous, "one home is registered for multiple Fleet identities"
		}
	}
	if len(mismatch) > 0 {
		return StateIdentityMismatch, strings.Join(mismatch, "; ")
	}
	if valid == 1 {
		return StateReady, ""
	}
	if len(unreadable) > 0 {
		return StateUnreadable, strings.Join(unreadable, "; ")
	}
	if missing == len(observations) {
		return StateMissing, "all known Fleet homes are missing"
	}
	return StateUnreadable, "Fleet identity could not be observed"
}

func lastKnownHome(db *sql.DB, id string, locations []string) string {
	var home string
	if err := db.QueryRow(`SELECT last_known_home FROM fleet_registry WHERE fleet_id = ?`, id).Scan(&home); err == nil && home != "" {
		return home
	}
	if len(locations) > 0 {
		return locations[0]
	}
	return ""
}

type DuplicateError struct {
	FleetID string
	Current string
	Other   []string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("Fleet %s is also valid at %s; copying a Fleet directory does not create a new Fleet identity, so operation refused for %s", e.FleetID, strings.Join(e.Other, ", "), e.Current)
}

type AmbiguousError struct {
	Current string
	Reason  string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("Fleet home %s has ambiguous registry identity: %s", e.Current, e.Reason)
}

func (r *Registry) Check(home, fleetID string) error {
	if err := store.ValidateFleetID(fleetID); err != nil {
		return err
	}
	current, err := canonicalPath(home)
	if err != nil {
		return err
	}
	locators, err := r.Locators()
	if err != nil {
		return err
	}
	var other []string
	var claims []string
	for _, locator := range locators {
		if samePath(locator.Home, current) {
			if locator.FleetID != fleetID {
				claims = append(claims, locator.FleetID)
			}
			continue
		}
		if locator.FleetID != fleetID {
			continue
		}
		observed := observe(locator)
		if observed.valid {
			other = append(other, locator.Home)
		}
	}
	if len(claims) > 0 {
		return &AmbiguousError{Current: current, Reason: "registered as " + strings.Join(claims, ", ") + " and does not match its authoritative Fleet identity"}
	}
	if len(other) > 0 {
		slices.Sort(other)
		return &DuplicateError{FleetID: fleetID, Current: current, Other: other}
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if os.IsNotExist(err) {
		return abs, nil
	}
	return "", err
}

func pathKey(path string) (string, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(canonical), nil
	}
	return canonical, nil
}

func samePath(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	leftKey, err := pathKey(left)
	if err != nil {
		return false
	}
	rightKey, err := pathKey(right)
	if err != nil {
		return false
	}
	return leftKey == rightKey
}

func (r *Registry) withWriteLock(fn func() error) error {
	path := r.path + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open registry lock: %w", err)
	}
	if err := filelock.Lock(file, true); err != nil {
		_ = file.Close()
		return fmt.Errorf("lock registry: %w", err)
	}
	defer func() {
		_ = filelock.Unlock(file)
		_ = file.Close()
	}()
	return fn()
}
