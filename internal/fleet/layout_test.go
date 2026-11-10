package fleet_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"luvus": func(args []string) int {
		if len(args) > 0 && args[0] == "--version" {
			fmt.Println("luvus " + fakebin.Params()["version"])
			return 0
		}
		return 1
	}})
	os.Exit(m.Run())
}

var layoutNow = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

type oldLayout struct {
	root, home, id string
	pin            luvus.Pin
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureOldLayout(t *testing.T) oldLayout {
	t.Helper()
	root, fleetHome := t.TempDir(), t.TempDir()
	bin := filepath.Join(t.TempDir(), "luvus")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, fakebin.Embed(t, "luvus", map[string]string{"version": "0.14.3"}), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := luvus.Keep(context.Background(), fleet.OldLuvusStore(root), bin, os.Environ(), layoutNow)
	if err != nil {
		t.Fatal(err)
	}
	id := "f0123456789ab"
	writeFile(t, filepath.Join(fleet.OldBackups(root, id), "hand.db.20261101T010203"), "update backup")
	writeFile(t, filepath.Join(fleet.OldBackups(root, id), "hand-20261102.db"), "daily snapshot")
	writeFile(t, filepath.Join(fleet.OldBackups(root, id), "hand-20261103.db.tmp"), "half written")
	writeFile(t, filepath.Join(root, "backups", "hand.20261101T010203"), "old hand binary")
	writeFile(t, filepath.Join(fleet.OldModels(root), "codex.json"), `{"format":1}`)
	writeFile(t, filepath.Join(fleet.OldModels(root), "codex.lock"), "")
	return oldLayout{root: root, home: fleetHome, id: id, pin: pin}
}

func (o oldLayout) migrate(t *testing.T) ([]string, error) {
	t.Helper()
	return fleet.Migrate(context.Background(), o.root, o.id, o.home, os.Environ(), layoutNow)
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateCopiesTheOldLayoutIntoTheFleetAndLeavesTheMachineSideAlone(t *testing.T) {
	o := fixtureOldLayout(t)
	oldPinFile := filepath.Join(fleet.OldLuvusStore(o.root), "pin.json")
	pinBefore, binBefore := readAll(t, oldPinFile), readAll(t, o.pin.Path)
	infoBefore, err := os.Stat(o.pin.Path)
	if err != nil {
		t.Fatal(err)
	}

	lines, err := o.migrate(t)
	if err != nil || len(lines) != 3 {
		t.Fatalf("Migrate = %q, %v", lines, err)
	}
	for i, want := range []string{"luvus 0.14.3 copied to " + fleet.LuvusStore(o.home), "backups: 2 files copied to " + fleet.Backups(o.home), "models: 1 files copied to " + fleet.ModelsCache(o.home)} {
		if !strings.HasPrefix(lines[i], want) {
			t.Fatalf("line %d = %q, want prefix %q", i, lines[i], want)
		}
	}

	pin, ok, err := luvus.LoadPin(fleet.LuvusStore(o.home))
	if err != nil || !ok || pin.Version != "0.14.3" || pin.SHA256 != o.pin.SHA256 || pin.Path == o.pin.Path || !strings.HasPrefix(pin.Path, fleet.LuvusStore(o.home)) {
		t.Fatalf("fleet pin = %+v %v %v", pin, ok, err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatal(err)
	}
	raw := readAll(t, filepath.Join(fleet.LuvusStore(o.home), "pin.json"))
	if strings.Contains(raw, o.root) || strings.Contains(raw, o.home) {
		t.Fatalf("the fleet's pin.json holds an absolute path:\n%s", raw)
	}
	if got := readAll(t, filepath.Join(fleet.Backups(o.home), "hand.db.20261101T010203")); got != "update backup" {
		t.Fatalf("update backup = %q", got)
	}
	if got := readAll(t, filepath.Join(fleet.Backups(o.home), "hand-20261102.db")); got != "daily snapshot" {
		t.Fatalf("snapshot = %q", got)
	}
	if got := names(t, fleet.Backups(o.home)); !slices.Equal(got, []string{".migrated", "hand-20261102.db", "hand.db.20261101T010203"}) {
		t.Fatalf("fleet backups = %q (a .tmp leftover must not travel)", got)
	}
	if got := names(t, fleet.ModelsCache(o.home)); !slices.Equal(got, []string{"codex.json"}) {
		t.Fatalf("fleet models = %q (a lock file must not travel)", got)
	}

	after, err := os.Stat(o.pin.Path)
	if err != nil || readAll(t, oldPinFile) != pinBefore || readAll(t, o.pin.Path) != binBefore || !after.ModTime().Equal(infoBefore.ModTime()) || !os.SameFile(after, infoBefore) {
		t.Fatalf("the machine copy of Luvus changed: %v", err)
	}
	if got := names(t, fleet.OldBackups(o.root, o.id)); len(got) != 3 {
		t.Fatalf("the machine backups were touched: %q", got)
	}
	if got := readAll(t, filepath.Join(o.root, "backups", "hand.20261101T010203")); got != "old hand binary" {
		t.Fatalf("the machine's binary backup = %q", got)
	}
	if server, match := luvus.Match(pin, true, "0.14.3", nil); server != "0.14.3" || match != "yes" {
		t.Fatalf("a server started from the machine copy no longer matches the fleet's pin: %s %s", server, match)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func TestMigrateIsIdempotentAndNeverOverwritesTheFleet(t *testing.T) {
	o := fixtureOldLayout(t)
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(fleet.Backups(o.home), "hand-20261102.db")
	writeFile(t, mine, "the fleet's own newer copy")
	pinFile := filepath.Join(fleet.LuvusStore(o.home), "pin.json")
	pinBefore := readAll(t, pinFile)
	lines, err := o.migrate(t)
	if err != nil || len(lines) != 0 {
		t.Fatalf("second Migrate = %q, %v", lines, err)
	}
	if readAll(t, mine) != "the fleet's own newer copy" || readAll(t, pinFile) != pinBefore {
		t.Fatal("a second run rewrote what the fleet already holds")
	}
}

func TestMigrateBringsOnlyWhatIsMissingUntilItCompletedOnce(t *testing.T) {
	o := fixtureOldLayout(t)
	mine := filepath.Join(fleet.Backups(o.home), "hand-20261102.db")
	writeFile(t, mine, "the fleet's own")
	lines, err := o.migrate(t)
	if err != nil || len(lines) != 3 || !strings.HasPrefix(lines[1], "backups: 1 files copied") {
		t.Fatalf("Migrate = %q, %v", lines, err)
	}
	late := filepath.Join(fleet.OldBackups(o.root, o.id), "hand-20261104.db")
	writeFile(t, late, "written by a rolled-back hand after the first run")
	if lines, err = o.migrate(t); err != nil || len(lines) != 0 {
		t.Fatalf("second Migrate = %q, %v", lines, err)
	}
	if got := fleet.RestorePoints(o.root, o.id, o.home); !slices.Contains(got, late) {
		t.Fatalf("RestorePoints = %q lacks the machine-side backup", got)
	}
}

func TestMigrateWithNothingOldDoesNothing(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	lines, err := fleet.Migrate(context.Background(), root, "f0123456789ab", home, os.Environ(), layoutNow)
	if err != nil || len(lines) != 0 {
		t.Fatalf("Migrate = %q, %v", lines, err)
	}
	if got, _ := os.ReadDir(home); len(got) != 0 {
		t.Fatalf("an empty migration wrote %v", got)
	}
}

func TestMigrateDoesNotCopyAChangedMachineBinary(t *testing.T) {
	o := fixtureOldLayout(t)
	if err := os.Chmod(o.pin.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(o.pin.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.pin.Path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := o.migrate(t)
	if err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("err = %v", err)
	}
	if _, ok, _ := luvus.LoadPin(fleet.LuvusStore(o.home)); ok {
		t.Fatal("a pin was written from a changed binary")
	}
}

func TestLoadPinFallsBackToTheMachineCopyUntilTheFleetHasOne(t *testing.T) {
	o := fixtureOldLayout(t)
	pin, ok, err := fleet.LoadPin(o.root, o.home)
	if err != nil || !ok || pin.Path != o.pin.Path {
		t.Fatalf("before the migration = %+v %v %v", pin, ok, err)
	}
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	pin, ok, err = fleet.LoadPin(o.root, o.home)
	if err != nil || !ok || !strings.HasPrefix(pin.Path, fleet.LuvusStore(o.home)) {
		t.Fatalf("after the migration = %+v %v %v", pin, ok, err)
	}
	if pin, ok, err = fleet.LoadPin(o.root, ""); err != nil || !ok || pin.Path != o.pin.Path {
		t.Fatalf("outside a fleet = %+v %v %v", pin, ok, err)
	}
}

func TestTheFleetsPinResolvesAfterTheFleetFolderMoves(t *testing.T) {
	o := fixtureOldLayout(t)
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "mounted-elsewhere")
	if err := os.Rename(o.home, moved); err != nil {
		t.Skipf("cannot move the folder: %v", err)
	}
	if err := os.RemoveAll(fleet.OldLuvusStore(o.root)); err != nil {
		t.Fatal(err)
	}
	pin, ok, err := fleet.LoadPin(o.root, moved)
	if err != nil || !ok || !strings.HasPrefix(pin.Path, moved) {
		t.Fatalf("LoadPin = %+v %v %v", pin, ok, err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatal(err)
	}
	if v, err := luvus.BinaryVersion(context.Background(), pin.Path, os.Environ()); err != nil || v != "0.14.3" {
		t.Fatalf("the moved copy runs as %q, %v", v, err)
	}
}

func TestRestorePointsFindOldAndNewBackupsNewestFirst(t *testing.T) {
	o := fixtureOldLayout(t)
	oldDir := fleet.OldBackups(o.root, o.id)
	for name, at := range map[string]time.Time{
		"hand.db.20261101T010203": layoutNow.Add(-72 * time.Hour),
		"hand-20261102.db":        layoutNow.Add(-48 * time.Hour),
	} {
		if err := os.Chtimes(filepath.Join(oldDir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	newest := filepath.Join(fleet.Backups(o.home), "hand.db.20261109T010203")
	writeFile(t, newest, "after the move")
	if err := os.Chtimes(newest, layoutNow.Add(-time.Hour), layoutNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := []string{newest, filepath.Join(oldDir, "hand-20261102.db"), filepath.Join(oldDir, "hand.db.20261101T010203")}
	if got := fleet.RestorePoints(o.root, o.id, o.home); !slices.Equal(got, want) {
		t.Fatalf("RestorePoints = %q, want %q", got, want)
	}
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	if got := fleet.RestorePoints(o.root, o.id, o.home); len(got) != 3 || got[0] != newest {
		t.Fatalf("after the copy each backup is listed once: %q", got)
	}
	if bytes.Contains([]byte(strings.Join(fleet.RestorePoints(o.root, o.id, o.home), "\n")), []byte("hand.20261101")) {
		t.Fatal("the hand binary backup is not a database restore point")
	}
}

func TestMigrateCopiesBackupsOnceSoAPrunedOneStaysGone(t *testing.T) {
	o := fixtureOldLayout(t)
	newest := filepath.Join(fleet.Backups(o.home), "hand.db.20261110T000000")
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	writeFile(t, newest, "after the update")
	for _, name := range []string{"hand.db.20261101T010203", "hand-20261102.db"} {
		if err := os.Remove(filepath.Join(fleet.Backups(o.home), name)); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := o.migrate(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "backups:") {
			t.Fatalf("the pruned backups were copied again: %q", lines)
		}
	}
	got := fleet.RestorePoints(o.root, o.id, o.home)
	if len(got) == 0 || got[0] != newest {
		t.Fatalf("RestorePoints = %q, newest should be %s", got, newest)
	}
	if _, err := os.Lstat(filepath.Join(fleet.Backups(o.home), "hand-20261102.db")); err == nil {
		t.Fatal("a pruned snapshot came back")
	}
}

func TestMigrateKeepsTheModTimeOfACopiedFile(t *testing.T) {
	o := fixtureOldLayout(t)
	src := filepath.Join(fleet.OldBackups(o.root, o.id), "hand.db.20261101T010203")
	at := layoutNow.Add(-240 * time.Hour)
	if err := os.Chtimes(src, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := o.migrate(t); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(fleet.Backups(o.home), "hand.db.20261101T010203"))
	if err != nil || !fi.ModTime().Equal(at) {
		t.Fatalf("copied mtime = %v, %v; want %v", fi.ModTime(), err, at)
	}
}

func TestRestorePointsSortByTheStampInTheNameNotTheModTime(t *testing.T) {
	o := fixtureOldLayout(t)
	dir := fleet.Backups(o.home)
	older, newer := filepath.Join(dir, "hand.db.20261105T000000"), filepath.Join(dir, "hand-20261108.db")
	writeFile(t, older, "a")
	writeFile(t, newer, "b")
	if err := os.Chtimes(newer, layoutNow.Add(-48*time.Hour), layoutNow.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(older, layoutNow, layoutNow); err != nil {
		t.Fatal(err)
	}
	if got := fleet.RestorePoints(o.root, o.id, o.home); len(got) < 2 || got[0] != newer || got[1] != older {
		t.Fatalf("RestorePoints = %q", got)
	}
}
