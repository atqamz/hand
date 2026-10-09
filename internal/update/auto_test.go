package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
)

func TestRunRefusesWhileAnotherRunHoldsTheLock(t *testing.T) {
	f := newCycle(t)
	lock, ok, err := lockFile(f.root)
	if err != nil || !ok {
		t.Fatalf("lock = %v, %v", ok, err)
	}
	if running, err := Running(f.root); err != nil || !running {
		t.Fatalf("Running = %v, %v", running, err)
	}
	if _, err := Run(context.Background(), f.o); !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), "another hand update is running") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
	f.o.Check = true
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatalf("check was refused: %v", err)
	}
	_ = flock.Release(lock)
	if running, err := Running(f.root); err != nil || running {
		t.Fatalf("Running after release = %v, %v", running, err)
	}
	f.o.Check = false
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if running, _ := Running(f.root); running {
		t.Fatal("Run kept the lock")
	}
}

func TestKeepLuvusReturnsEarlyWhenTheBinaryIsCurrent(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.stale()
	f.watcherRuns(t)
	f.boardRuns(t)
	f.o.KeepLuvus = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.processCalls(t); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
	if len(rep.Units) != 0 {
		t.Fatalf("units = %+v", rep.Units)
	}
	f.unchanged(t)
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestKeepLuvusUpdatesAndLeavesTheSwitchPending(t *testing.T) {
	f := newCycle(t)
	f.stale()
	f.watcherRuns(t)
	f.boardRuns(t)
	f.o.KeepLuvus = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"kill", "watcher @ " + f.alpha, "kill", "board @ " + f.alpha, "hand init @ " + f.alpha}
	if got := f.processCalls(t); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if got := alphaLuvus(t, rep); got != "pending: --keep-luvus" {
		t.Fatalf("luvus = %q", got)
	}
	for _, u := range rep.Units {
		if strings.HasPrefix(u.Name, "luvus ") {
			t.Fatalf("units = %+v", rep.Units)
		}
	}
	if !slices.ContainsFunc(rep.Help, func(h string) bool { return h == pendingLine }) {
		t.Fatalf("help = %q", rep.Help)
	}
}

func TestJournalSaveWritesItsContentDurably(t *testing.T) {
	root := t.TempDir()
	j := journal{Watcher: []string{"w"}, Supervisor: []string{"s"}, Board: []string{"b"}, Init: []string{"i"}}
	for range 2 {
		if err := j.save(root); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := json.Marshal(j)
	if got, err := os.ReadFile(journalPath(root)); err != nil || string(got) != string(want) {
		t.Fatalf("journal = %q, %v; want %q", got, err, want)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("folder holds %d entries, want only update.json", len(entries))
	}
	if err := syncDir(root); err != nil {
		t.Fatal(err)
	}
}
