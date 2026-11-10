//go:build unix

package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
)

func TestMigratingTheLayoutNeverMovesOrStopsWhatARunningServerUses(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.serverRuns(t)
	f.watcherRuns(t)
	oldPin := filepath.Join(fleet.OldLuvusStore(f.root), "pin.json")
	pinBefore, err := os.ReadFile(oldPin)
	if err != nil {
		t.Fatal(err)
	}
	binBefore, err := os.ReadFile(f.pin.Path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(f.pin.Path)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if len(rep.Migrated) != 1 || !strings.HasPrefix(rep.Migrated[0], "alpha: luvus "+f.pin.Version+" copied to "+fleet.LuvusStore(f.alpha)) {
		t.Fatalf("migrated = %q", rep.Migrated)
	}
	if got := f.log(t, "kill", "force", "luvus start", " stop ", "watcher @ ", "board @ "); len(got) != 0 {
		t.Fatalf("the migration stopped or started something: %q", got)
	}
	if pinAfter, err := os.ReadFile(oldPin); err != nil || string(pinAfter) != string(pinBefore) {
		t.Fatalf("the machine pin.json changed: %v", err)
	}
	after, err := os.Stat(f.pin.Path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the machine binary was moved or replaced: %v", err)
	}
	if binAfter, err := os.ReadFile(f.pin.Path); err != nil || string(binAfter) != string(binBefore) {
		t.Fatalf("the machine binary changed: %v", err)
	}
	pin, ok, err := luvus.LoadPin(fleet.LuvusStore(f.alpha))
	if err != nil || !ok || pin.Path == f.pin.Path || pin.SHA256 != f.pin.SHA256 {
		t.Fatalf("fleet pin = %+v %v %v", pin, ok, err)
	}
	if out := Render(rep).String(); !strings.Contains(out, "migrated[1]:") || !strings.Contains(out, "luvus: "+f.pin.Version+" kept\n") {
		t.Fatalf("render:\n%s", out)
	}
	again, err := Run(context.Background(), f.o)
	if err != nil || len(again.Migrated) != 0 || again.Status != "up to date" {
		t.Fatalf("second run = %+v, %v", again, err)
	}
}

func TestCheckMigratesNothing(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || len(rep.Migrated) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if _, ok, _ := luvus.LoadPin(fleet.LuvusStore(f.alpha)); ok {
		t.Fatal("--check pinned a copy into the fleet")
	}
	if _, err := os.Stat(fleet.Backups(f.alpha)); err == nil {
		t.Fatal("--check made a backups folder")
	}
}

func TestEachFleetGetsItsOwnNewerPin(t *testing.T) {
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	_, a := newFleet(t, f.root, "alpha")
	_, b := newFleet(t, f.root, "beta")
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, home := range []string{a, b} {
		pin, ok, err := luvus.LoadPin(fleet.LuvusStore(home))
		if err != nil || !ok || pin.Version != "0.14.4" || !strings.HasPrefix(pin.Path, fleet.LuvusStore(home)) {
			t.Fatalf("%s pin = %+v %v %v", home, pin, ok, err)
		}
		seen = append(seen, pin.Path)
	}
	if seen[0] == seen[1] || rep.PinFrom != "0.14.3" || rep.PinTo != "0.14.4" {
		t.Fatalf("pins = %q, report = %+v", seen, rep)
	}
	if old, _, _ := luvus.LoadPin(fleet.OldLuvusStore(f.root)); old.Version != "0.14.3" {
		t.Fatalf("the machine pin changed: %+v", old)
	}
}

func fleetNamed(t *testing.T, rep Report, name string) FleetResult {
	t.Helper()
	for _, fr := range rep.Fleets {
		if fr.Name == name {
			return fr
		}
	}
	t.Fatalf("no %s in %+v", name, rep.Fleets)
	return FleetResult{}
}

func TestCheckPlansTheSwitchPerFleetFromItsOwnPin(t *testing.T) {
	f := newCycle(t)
	f.current()
	_, beta := newFleet(t, f.root, "beta")
	keepLuvusIn(t, fleet.LuvusStore(f.alpha), "0.14.3")
	keepLuvusIn(t, fleet.LuvusStore(beta), "0.14.4")
	f.mu.Lock()
	f.version = "0.14.4"
	f.mu.Unlock()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := fleetNamed(t, rep, "beta").Luvus; got != "kept" {
		t.Fatalf("beta already holds the release's Luvus; report says %q", got)
	}
}

func TestOneFleetsBadPinDoesNotStopTheOthers(t *testing.T) {
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	_, a := newFleet(t, f.root, "alpha")
	_, b := newFleet(t, f.root, "beta")
	store := fleet.LuvusStore(a)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "pin.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed || !strings.HasPrefix(fleetNamed(t, rep, "alpha").Luvus, "failed: ") {
		t.Fatalf("report = %+v", rep)
	}
	if pin, ok, err := luvus.LoadPin(fleet.LuvusStore(b)); err != nil || !ok || pin.Version != "0.14.4" {
		t.Fatalf("beta pin = %+v %v %v", pin, ok, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(store, "pin.json")); string(raw) != "not json" {
		t.Fatalf("alpha's pin.json was rewritten: %q", raw)
	}
}

func TestAPinThatCannotBeKeptForOneFleetDoesNotStopTheOthers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	_, a := newFleet(t, f.root, "alpha")
	_, b := newFleet(t, f.root, "beta")
	locked := filepath.Join(a, "luvus")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed || !strings.HasPrefix(fleetNamed(t, rep, "alpha").Luvus, "failed: ") {
		t.Fatalf("report = %+v", rep)
	}
	if pin, ok, err := luvus.LoadPin(fleet.LuvusStore(b)); err != nil || !ok || pin.Version != "0.14.4" {
		t.Fatalf("beta pin = %+v %v %v", pin, ok, err)
	}
}
