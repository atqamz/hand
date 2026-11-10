package cli_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
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

func asChannel(t *testing.T, channel string) {
	t.Helper()
	old := cli.Channel
	cli.Channel = channel
	t.Cleanup(func() { cli.Channel = old })
}

func asVersion(t *testing.T, version string) {
	t.Helper()
	old := cli.Version
	cli.Version = version
	t.Cleanup(func() { cli.Version = old })
}

func TestTheRefusalNamesACommandThatReachesTheEdgePin(t *testing.T) {
	for channel, want := range map[string]string{
		"stable": "Run `hand update --channel edge` to get",
		"edge":   "Run `hand update` to get",
		"source": "Run `hand update --channel stable` to get",
	} {
		t.Run(channel, func(t *testing.T) {
			asChannel(t, channel)
			h, home, _ := initMoveFleet(t)
			putPin(t, home, "99.0.0", state.SchemaVersion)
			refuses(t, h, want)
		})
	}
}

func TestAnUnparseableVersionNeverRefusesOnItsOwn(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	putPin(t, home, "99.0.0-rc1", state.SchemaVersion)
	h.ok("orient")
	asVersion(t, "0.0.0-e2e")
	putPin(t, home, "9.9.9", state.SchemaVersion)
	h.ok("orient")
	if p := readPin(t, home); p.Version != "0.0.0-e2e" {
		t.Fatalf("pin = %+v", p)
	}
	putPin(t, home, "99.0.0-rc1", state.SchemaVersion+1)
	refuses(t, h, fmt.Sprintf("schema %d", state.SchemaVersion+1))
}

func TestAnUnreadablePinWarnsAndIsLeftAlone(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":  `{"version":"99.0.0","sch`,
		"empty":      "",
		"null":       "null",
		"no version": `{"schema":23}`,
	} {
		t.Run(name, func(t *testing.T) {
			h, home, _ := initMoveFleet(t)
			path := filepath.Join(home, fleet.VersionFile)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"orient"}, {"init"}} {
				if _, errOut, code := h.run(args...); code != 0 || !strings.Contains(errOut, "warning: cannot use") || !strings.Contains(errOut, fleet.VersionFile) {
					t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
				}
			}
			if b, _ := os.ReadFile(path); string(b) != body {
				t.Fatalf("an open rewrote an unreadable pin to %q", b)
			}
			out, _, _ := h.run("doctor")
			if !strings.Contains(out, "hand.version,warn") {
				t.Fatalf("doctor:\n%s", out)
			}
		})
	}
}

func TestWritePinNeverLowersANewerPin(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	env := h.env(&bytes.Buffer{}, &bytes.Buffer{})
	putPin(t, home, "99.0.0", state.SchemaVersion+1)
	cli.WritePin(env, home)
	if p := readPin(t, home); p.Version != "99.0.0" {
		t.Fatalf("pin = %+v", p)
	}
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	cli.WritePin(env, home)
	if p := readPin(t, home); p.Version != cli.Version {
		t.Fatalf("pin = %+v", p)
	}
}

func TestTakeOverRejudgesAMarkerThatChanged(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	env := h.env(&bytes.Buffer{}, &bytes.Buffer{})
	asHost(t, "desk", "boot-b")
	stale := fleet.HostMarker{Host: "laptop", Boot: "boot-x", PID: 31337, At: h.clock().Add(-time.Hour)}
	putMarker(t, h, "laptop", "boot-x", time.Second)
	fresh := markerBytes(t, h)
	events := eventCount(t, home, "")
	if err := cli.TakeOver(env, home, stale); err == nil || !strings.Contains(err.Error(), "open on host laptop") {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(markerBytes(t, h), fresh) || eventCount(t, home, "") != events {
		t.Fatal("a takeover judged on a stale marker removed the fresh one")
	}
	if err := os.Remove(filepath.Join(home, fleet.HostFile)); err != nil {
		t.Fatal(err)
	}
	if err := cli.TakeOver(env, home, stale); err != nil || eventCount(t, home, "") != events {
		t.Fatalf("a vanished marker: err=%v events=%d", err, eventCount(t, home, "")-events)
	}
	if err := fleet.WriteHost(home, stale); err != nil {
		t.Fatal(err)
	}
	if err := cli.TakeOver(env, home, stale); err != nil || eventCount(t, home, "fleet.took_over") != 1 || markerBytes(t, h) != nil {
		t.Fatalf("an unchanged stale marker was not taken over: %v", err)
	}
}

func TestTheBoardRefusesAFleetPinnedNewerAndWritesNothingInIt(t *testing.T) {
	h, home, id := initMoveFleet(t)
	putPin(t, home, "99.0.0", state.SchemaVersion)
	before := snapshotTree(t, home)
	base, _ := startBoard(t, h, "127.0.0.1")
	res, err := http.Get(base + "/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode == http.StatusOK || !strings.Contains(string(body), "99.0.0") {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	for rel, f := range snapshotTree(t, home) {
		want, had := before[rel]
		switch {
		case !had && (rel == "hand.db-wal" || rel == "hand.db-shm"):
		case !had:
			t.Errorf("the board added %s to a refused fleet", rel)
		case rel != "hand.db-wal" && rel != "hand.db-shm" && f != want:
			t.Errorf("the board changed %s in a refused fleet", rel)
		}
	}
}

func TestTheBoardMigratesAnOlderFleetAndRepinsIt(t *testing.T) {
	h, home, id := initMoveFleet(t)
	asOldSchema(t, home, state.SchemaVersion-1)
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	base, _ := startBoard(t, h, "127.0.0.1")
	res, err := http.Get(base + "/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if got := userVersion(t, home); got != state.SchemaVersion {
		t.Fatalf("schema = %d", got)
	}
	if p := readPin(t, home); p.Version != cli.Version || p.Schema != state.SchemaVersion {
		t.Fatalf("pin = %+v", p)
	}
}

func TestFleetAdoptAndMoveRefuseAPinnedNewerFleetAndUnlockStaysOpen(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	putPin(t, home, "99.0.0", state.SchemaVersion)
	dest := filepath.Join(filepath.Dir(home), "elsewhere")
	before := snapshotTree(t, home)
	for _, args := range [][]string{{"fleet", "adopt"}, {"fleet", "move", "--yes", dest}} {
		if _, errOut, code := h.run(args...); code != 2 || !strings.Contains(errOut, "`hand update") || !strings.Contains(errOut, "99.0.0") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	assertTreeUnchanged(t, before, home)
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("a refused move created the destination")
	}
	for _, args := range [][]string{{"fleet", "adopt", "--check"}, {"fleet", "move", "--dry-run", dest}} {
		if _, errOut, code := h.run(args...); code != 0 {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", time.Hour)
	if out := h.ok("fleet", "unlock"); !strings.Contains(out, "unlock: cleared") {
		t.Fatalf("unlock = %q", out)
	}
}

func TestFleetAdoptAndMoveRepinAnOlderFleet(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	h.ok("fleet", "adopt")
	if p := readPin(t, home); p.Version != cli.Version {
		t.Fatalf("adopt left the pin at %+v", p)
	}
	putPin(t, home, "0.1.0", state.SchemaVersion-1)
	dest := filepath.Join(filepath.Dir(home), "elsewhere")
	h.ok("fleet", "move", "--yes", dest)
	if p := readPin(t, dest); p.Version != cli.Version {
		t.Fatalf("move left the pin at %+v", p)
	}
}
