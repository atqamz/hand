package cli_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func putPin(t *testing.T, home, version string, schema int) {
	t.Helper()
	if err := fleet.WriteVersion(home, fleet.VersionPin{Version: version, Commit: "abc1234", Schema: schema}); err != nil {
		t.Fatal(err)
	}
}

func readPin(t *testing.T, home string) fleet.VersionPin {
	t.Helper()
	p, found, err := fleet.ReadVersion(home)
	if err != nil || !found {
		t.Fatalf("hand.version: found=%v err=%v", found, err)
	}
	return p
}

func asOldSchema(t *testing.T, home string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE alert`, fmt.Sprintf(`PRAGMA user_version = %d`, version)} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func userVersion(t *testing.T, home string) int {
	t.Helper()
	return adoptCount(t, home, `PRAGMA user_version`)
}

func refuses(t *testing.T, h *harness, want ...string) {
	t.Helper()
	before := snapshotTree(t, h.home)
	for _, args := range [][]string{{"orient"}, {"init"}, {"task", "list"}} {
		_, errOut, code := h.run(args...)
		if code != 2 {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
		for _, w := range want {
			if !strings.Contains(errOut, w) {
				t.Fatalf("%q: stderr %q lacks %q", args, errOut, w)
			}
		}
	}
	assertTreeUnchanged(t, before, h.home)
	if got := len(snapshotTree(t, h.home)); got != len(before) {
		t.Fatalf("a refused open added %d files", got-len(before))
	}
}

func TestInitWritesThePin(t *testing.T) {
	_, home, _ := initMoveFleet(t)
	if p := readPin(t, home); p.Version != cli.Version || p.Schema != state.SchemaVersion {
		t.Fatalf("pin = %+v", p)
	}
}

func TestPinOfANewerVersionRefusesAndNamesTheUpdateCommand(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	putPin(t, home, "99.0.0", state.SchemaVersion)
	refuses(t, h, "99.0.0", cli.Version, "`hand update", "nothing was changed")
}

func TestPinOfANewerSchemaRefusesAndNamesTheUpdateCommand(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	putPin(t, home, cli.Version, state.SchemaVersion+1)
	refuses(t, h, fmt.Sprintf("schema %d", state.SchemaVersion+1), fmt.Sprintf("schema %d", state.SchemaVersion), "`hand update")
}

func TestRefusedOpenLeavesAnOldSchemaDatabaseUnmigrated(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asOldSchema(t, home, state.SchemaVersion-1)
	putPin(t, home, "99.0.0", state.SchemaVersion+3)
	refuses(t, h, "`hand update")
	if got := userVersion(t, home); got != state.SchemaVersion-1 {
		t.Fatalf("a refused open migrated the database to schema %d", got)
	}
}

func TestRefusedHostOpenLeavesAnOldSchemaDatabaseUnmigrated(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asOldSchema(t, home, state.SchemaVersion-1)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	before := snapshotTree(t, home)
	for _, args := range [][]string{{"orient"}, {"init"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "open on host laptop") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	assertTreeUnchanged(t, before, home)
	if got := userVersion(t, home); got != state.SchemaVersion-1 {
		t.Fatalf("a refused open migrated the database to schema %d", got)
	}
}

func TestOldSchemaCopyOnANewerHandMigratesAndRepins(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asOldSchema(t, home, state.SchemaVersion-1)
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	h.ok("orient")
	if got := userVersion(t, home); got != state.SchemaVersion {
		t.Fatalf("schema = %d, want %d", got, state.SchemaVersion)
	}
	if p := readPin(t, home); p.Version != cli.Version || p.Schema != state.SchemaVersion {
		t.Fatalf("pin = %+v", p)
	}
}

func TestOpenWritesAMissingPin(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	if err := os.Remove(filepath.Join(home, fleet.VersionFile)); err != nil {
		t.Fatal(err)
	}
	h.ok("orient")
	if p := readPin(t, home); p.Version != cli.Version || p.Schema != state.SchemaVersion {
		t.Fatalf("pin = %+v", p)
	}
}

func TestOpenKeepsAnUpToDatePinAsItIs(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	before := snapshotTree(t, home)[fleet.VersionFile]
	h.ok("orient")
	if after := snapshotTree(t, home)[fleet.VersionFile]; after != before {
		t.Fatal("an open rewrote a current pin")
	}
}

func TestDoctorRowForThePin(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	row := func() string {
		out, _, _ := h.run("doctor")
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "hand.version,") {
				return strings.TrimSpace(l)
			}
		}
		return ""
	}
	if got := row(); !strings.HasPrefix(got, "hand.version,ok") {
		t.Fatalf("row = %q", got)
	}
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	before := snapshotTree(t, home)
	if got := row(); !strings.HasPrefix(got, "hand.version,info") {
		t.Fatalf("older pin row = %q", got)
	}
	assertTreeUnchanged(t, before, home)
	putPin(t, home, "99.0.0", state.SchemaVersion+1)
	before = snapshotTree(t, home)
	if got := row(); !strings.HasPrefix(got, "hand.version,fail") || !strings.Contains(got, "hand update") {
		t.Fatalf("newer pin row = %q", got)
	}
	assertTreeUnchanged(t, before, home)
}
