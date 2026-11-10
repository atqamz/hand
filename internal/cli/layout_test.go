package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/toon"
)

func esc(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

func seedOldLayout(t *testing.T, h *harness, id string) luvus.Pin {
	t.Helper()
	root := h.vars["SECONDHAND_HOME"]
	pin, err := luvus.Keep(context.Background(), fleet.OldLuvusStore(root), fakeLuvusAt(t, t.TempDir(), "0.14.3"), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := fleet.OldBackups(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hand.db.20261101T010203", "hand-20261102.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return pin
}

func TestInitMakesTheNewLayoutInsideTheFleet(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	root := h.vars["SECONDHAND_HOME"]
	out := h.ok("init")
	store := fleet.LuvusStore(h.home)
	if !strings.HasPrefix(field(out, "luvus"), "pinned 0.14.3 ("+store) {
		t.Fatalf("init = %q", out)
	}
	for _, dir := range []string{fleet.Backups(h.home), fleet.ModelsCache(h.home), store} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", dir, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(store, "pin.json"))
	if err != nil || strings.Contains(string(raw), root) || strings.Contains(string(raw), h.home) || !strings.Contains(string(raw), `"path": "0.14.3-`) {
		t.Fatalf("pin.json = %s, %v", raw, err)
	}
	for _, p := range []string{"luvus", "backups", "models", "iroh"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Fatalf("a fresh init wrote %s on the machine side", p)
		}
	}
	if strings.Contains(out, "migrated") {
		t.Fatalf("a fresh init migrated something: %q", out)
	}
}

func TestInitOnAnOldLayoutCopiesAndSaysSo(t *testing.T) {
	h := newHarness(t)
	noLuvusOnPath(h, t)
	id := field(h.ok("init"), "id")
	old := seedOldLayout(t, h, id)
	before, err := os.ReadFile(old.Path)
	if err != nil {
		t.Fatal(err)
	}
	out := h.ok("init")
	has(t, "second init", out, "migrated[2]:", "luvus 0.14.3 copied to "+esc(fleet.LuvusStore(h.home)), "backups: 2 files copied to "+esc(fleet.Backups(h.home)), "luvus: 0.14.3 (pinned)")
	if after, err := os.ReadFile(old.Path); err != nil || string(after) != string(before) {
		t.Fatalf("the machine copy changed: %v", err)
	}
	if got := fleet.RestorePoints(h.vars["SECONDHAND_HOME"], id, h.home); len(got) != 2 || !strings.HasPrefix(got[0], fleet.Backups(h.home)) && !strings.HasPrefix(got[1], fleet.Backups(h.home)) {
		t.Fatalf("restore points = %q", got)
	}
	if again := h.ok("init"); strings.Contains(again, "migrated") {
		t.Fatalf("a second migration: %q", again)
	}
}

func TestTheFirstCommandInAFleetMigratesTheOldLayoutOnce(t *testing.T) {
	h := newHarness(t)
	noLuvusOnPath(h, t)
	id := field(h.ok("init"), "id")
	old := seedOldLayout(t, h, id)
	oldPinFile := filepath.Join(fleet.OldLuvusStore(h.vars["SECONDHAND_HOME"]), "pin.json")
	pinBefore, err := os.ReadFile(oldPinFile)
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, code := h.run("orient")
	if code != 0 || strings.Contains(out, "migrated") {
		t.Fatalf("code=%d stdout=%q", code, out)
	}
	has(t, "stderr", errOut, "hand: migrated luvus 0.14.3 copied to "+fleet.LuvusStore(h.home), "hand: migrated backups: 2 files copied to "+fleet.Backups(h.home))
	pin, ok, err := luvus.LoadPin(fleet.LuvusStore(h.home))
	if err != nil || !ok || pin.Version != "0.14.3" || pin.Path == old.Path {
		t.Fatalf("fleet pin = %+v %v %v", pin, ok, err)
	}
	if got, err := os.ReadFile(oldPinFile); err != nil || string(got) != string(pinBefore) {
		t.Fatalf("the machine pin.json changed: %v", err)
	}
	if _, err := os.Stat(old.Path); err != nil {
		t.Fatalf("the machine binary is gone: %v", err)
	}
	if _, errOut, _ := h.run("orient"); strings.Contains(errOut, "migrated") {
		t.Fatalf("the second command migrated again: %q", errOut)
	}
}

func TestAFleetMovedToAnotherMountKeepsItsPin(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	h.ok("init")
	moved := filepath.Join(t.TempDir(), "other-mount")
	old := h.home
	if err := os.Rename(old, moved); err != nil {
		t.Skipf("cannot move: %v", err)
	}
	h.home = moved
	pin, ok, err := luvus.LoadPin(fleet.LuvusStore(moved))
	if err != nil || !ok || !strings.HasPrefix(pin.Path, fleet.LuvusStore(moved)) || pin.Verify() != nil {
		t.Fatalf("pin after the move = %+v %v %v", pin, ok, err)
	}
	has(t, "init in the new place", h.ok("init"), "luvus: 0.14.3 (pinned)")
	has(t, "show", h.ok("luvus", "show"), "pin: "+toon.Value("0.14.3 "+pin.Path))
}

func TestDoctorShowsWhatIsMachineLocalAndWhatIsLeftOver(t *testing.T) {
	h := newHarness(t)
	noLuvusOnPath(h, t)
	id := field(h.ok("init"), "id")
	root := h.vars["SECONDHAND_HOME"]
	seedOldLayout(t, h, id)

	out, _, _ := h.run("doctor")
	rows := checkRows(t, out)
	if row := rows["luvus pin"]; row[0] != "warn" || !strings.Contains(row[1], "still the machine copy") {
		t.Fatalf("luvus pin before the migration = %q", row)
	}
	if row := rows["machine-local"]; row[0] != "info" || !strings.Contains(row[1], esc(root)) || !strings.Contains(row[1], "worktrees/") || !strings.Contains(row[1], "luvus/"+runtime.GOOS+"-"+runtime.GOARCH) {
		t.Fatalf("machine-local = %q", row)
	}
	if row := rows["restore points"]; row[0] != "info" || !strings.Contains(row[1], "2 hand.db backups, newest "+esc(fleet.OldBackups(root, id))) {
		t.Fatalf("restore points = %q", row)
	}

	h.ok("orient")
	out, _, _ = h.run("doctor")
	rows = checkRows(t, out)
	if row := rows["luvus pin"]; row[0] != "ok" {
		t.Fatalf("luvus pin after the migration = %q", row)
	}
	if row := rows["old machine copies"]; row[0] != "info" || !strings.Contains(row[1], "luvus/") || !strings.Contains(row[1], "backups/"+id+"/") || !strings.Contains(row[1], "never deletes") {
		t.Fatalf("old machine copies = %q", row)
	}
	if row := rows["restore points"]; !strings.Contains(row[1], "2 hand.db backups") {
		t.Fatalf("restore points count a migrated copy twice: %q", row)
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range rows {
			if !yield(k) {
				return
			}
		}
	})
	if !slices.Contains(names, "machine-local") {
		t.Fatalf("rows = %q", names)
	}
}
