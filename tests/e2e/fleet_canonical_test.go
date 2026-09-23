//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
)

func TestFleetReadsCanonicalIdentityAfterCutover(t *testing.T) {
	for _, name := range []string{"exact", "different identity", "missing singleton", "schema drift", "older legacy"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SECONDHAND_HOME", filepath.Join(home, ".secondhand"))
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
			r, err := registry.Open()
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Register(home, fleetID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}

			wantState := "ready"
			if name == "older legacy" {
				execFleetFixtureSQL(t, home, `PRAGMA user_version = 20`)
				wantState = "unreadable"
			} else {
				if err := os.Remove(store.Path(home)); err != nil {
					t.Fatal(err)
				}
				fixtureID := fleetID
				switch name {
				case "different identity":
					fixtureID = "f_00000000000000000000000000000000"
					wantState = "identity-mismatch"
				case "missing singleton":
					fixtureID = ""
					wantState = "identity-mismatch"
				case "schema drift":
					wantState = "unreadable"
				}
				createCanonicalFleetFixture(t, home, fixtureID)
				if name == "schema drift" {
					execFleetFixtureSQL(t, home, `CREATE TABLE unexpected(id TEXT)`)
				}
			}

			registryPath, err := registry.Path()
			if err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, path := range []string{store.Path(home), registryPath} {
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			got := runHand(t, home, "fleet")
			if got.code != 0 || !strings.Contains(got.stdout, fleetID+",") || !strings.Contains(got.stdout, ","+wantState+",") {
				t.Fatalf("fleet = %+v, want %s classified %s", got, fleetID, wantState)
			}
			for path, original := range before {
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(original, after) {
					t.Fatalf("fleet mutated %s", path)
				}
			}
			if name == "exact" {
				r, err := registry.Open()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = r.Close() }()
				if err := r.Register(home, fleetID, time.Now()); err != nil {
					t.Fatalf("register canonical Fleet: %v", err)
				}
			}
		})
	}
}

func TestInitRefusesCanonicalBeforeMutation(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(store.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	createCanonicalFleetFixture(t, home, "f_0123456789abcdef0123456789abcdef")
	seedPrivateRuntime(t, home)
	before := snapshotTree(t, home)
	got := runHand(t, home, "init", home)
	if got.code == 0 || !strings.Contains(got.stderr, "canonical v19 state cannot be opened by legacy commands") {
		t.Fatalf("init = %+v, want canonical family refusal", got)
	}
	for path, value := range snapshotTree(t, home) {
		if old, ok := before[path]; !ok || old != value {
			t.Logf("init changed %s", path)
		}
	}
	assertTreeUnchanged(t, home, before)
}

// The fixture represents the published cutover database, not a completed cutover.
func createCanonicalFleetFixture(t *testing.T, home, fleetID string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "store", "v19.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	execFleetFixtureSQL(t, home, string(ddl))
	if fleetID != "" {
		execFleetFixtureSQL(t, home, `INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,?,'2026-09-23T00:00:00Z')`, fleetID)
	}
}

func execFleetFixtureSQL(t *testing.T, home, query string, args ...any) {
	t.Helper()
	path := filepath.ToSlash(store.Path(home))
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
