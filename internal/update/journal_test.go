package update

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func TestRepairStartsTheRegisteredFleetWhenTwoBookmarksShareAFolder(t *testing.T) {
	root := t.TempDir()
	aID, aHome := newFleet(t, root, "alpha")
	bID, bHome := newFleet(t, root, "beta")
	fleets := []fleet.Entry{{ID: aID, Name: "alpha", Home: aHome}, {ID: bID, Name: "beta", Home: bHome}}
	slices.SortFunc(fleets, func(a, b fleet.Entry) int { return strings.Compare(a.ID, b.ID) })
	ok, stale := fleets[0], fleets[1]
	if err := os.WriteFile(filepath.Join(fleet.IndexDir(root), stale.ID), []byte(ok.Home+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := entries(t, root)
	if len(list) != 2 || list[0].State != "ok" || list[1].State != "moved" || list[0].Home != ok.Home || list[1].Home != ok.Home {
		t.Fatalf("fleet.List = %+v, want one ok and one moved bookmark at %s", list, ok.Home)
	}

	var started []fleet.Entry
	rep := &Report{}
	rep.repair(context.Background(), Options{
		Root: root,
		Watch: func(_ context.Context, e fleet.Entry) error {
			started = append(started, e)
			return nil
		},
	}, journal{Watcher: []string{ok.Home}})

	if len(started) != 1 || started[0].ID != ok.ID || started[0].Name != ok.Name || started[0].Home != ok.Home || started[0].State != "ok" {
		t.Fatalf("watcher entries = %+v, want the ok bookmark %+v", started, ok)
	}
	if rep.Failed || len(rep.Help) != 0 {
		t.Fatalf("repair = failed %v, help %q", rep.Failed, rep.Help)
	}
}

func TestRepairNamesAnUnregisteredHomeOnce(t *testing.T) {
	root := t.TempDir()
	newFleet(t, root, "alpha")
	gone := filepath.Join(t.TempDir(), "gone")
	boards, watches := 0, 0
	rep := &Report{}
	rep.repair(context.Background(), Options{
		Root:  root,
		Board: func(context.Context, fleet.Entry) error { boards++; return nil },
		Watch: func(context.Context, fleet.Entry) error { watches++; return nil },
	}, journal{Board: []string{gone}, Watcher: []string{gone}})

	if boards != 0 || watches != 0 {
		t.Fatalf("started %d boards and %d watchers for %s", boards, watches, gone)
	}
	if !rep.Failed {
		t.Fatal("repair did not fail for an unregistered home")
	}
	if len(rep.Help) != 1 || !strings.Contains(rep.Help[0], gone) {
		t.Fatalf("help = %q, want one line naming %s", rep.Help, gone)
	}
	if !strings.Contains(rep.Help[0], "start it by hand") {
		t.Fatalf("help = %q, want the start-it-by-hand line", rep.Help)
	}
}
