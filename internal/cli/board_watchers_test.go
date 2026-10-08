package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	hpolicy "github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func sweepFast(t *testing.T) {
	t.Helper()
	old := *cli.WatcherSweep
	*cli.WatcherSweep = 30 * time.Millisecond
	t.Cleanup(func() { *cli.WatcherSweep = old })
}

func siblingFleet(t *testing.T, h *harness) *harness {
	t.Helper()
	o := newHarness(t)
	o.vars["SECONDHAND_HOME"] = h.vars["SECONDHAND_HOME"]
	o.ok("init")
	return o
}

func withSupervisor(t *testing.T, h *harness, end bool) {
	t.Helper()
	ctx := context.Background()
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}, Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if end {
		if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "1", TerminalID: "t", PaneID: "p", PID: 1, StartMarker: "m"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.EndSupervisor(ctx, sup.ID, state.AttemptExited, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func withAutoresume(t *testing.T, h *harness) {
	t.Helper()
	p := hpolicy.StarterPolicy
	p.Supervisor.Autoresume = true
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, hpolicy.PolicyFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func settle() { time.Sleep(300 * time.Millisecond) }

func TestBoardStartsTheWatcherOfAFleetWithALiveSupervisor(t *testing.T) {
	sweepFast(t)
	a := newHarness(t)
	a.ok("init")
	b := siblingFleet(t, a)
	withSupervisor(t, a, false)
	enableWatcher(t, a.home)
	enableWatcher(t, b.home)
	_, stop := startBoard(t, a, "127.0.0.1")
	eventually(t, func() bool { return watchSpawns(a.home) == 1 })
	settle()
	stop()
	if n := watchSpawns(a.home); n != 1 {
		t.Fatalf("a running watcher was started again: spawns = %d", n)
	}
	if n := watchSpawns(b.home); n != 0 {
		t.Fatalf("a fleet without a supervisor got a watcher: spawns = %d", n)
	}
}

func TestBoardLeavesAHeldWatcherAlone(t *testing.T) {
	sweepFast(t)
	h := newHarness(t)
	h.ok("init")
	withSupervisor(t, h, false)
	enableWatcher(t, h.home)
	holdWatchLock(t, h.home)
	_, stop := startBoard(t, h, "127.0.0.1")
	settle()
	stop()
	if n := watchSpawns(h.home); n != 0 {
		t.Fatalf("spawns = %d, want 0", n)
	}
}

func TestBoardKeepsTheWatcherOfAnAutoresumeFleet(t *testing.T) {
	sweepFast(t)
	a := newHarness(t)
	a.ok("init")
	b := siblingFleet(t, a)
	c := siblingFleet(t, a)
	for _, h := range []*harness{a, b, c} {
		withAutoresume(t, h)
		enableWatcher(t, h.home)
	}
	withSupervisor(t, a, true)
	_, stop := startBoard(t, a, "127.0.0.1")
	eventually(t, func() bool { return watchSpawns(a.home) == 1 })
	settle()
	stop()
	if n := watchSpawns(b.home) + watchSpawns(c.home); n != 0 {
		t.Fatalf("fleets with nothing to resume got watchers: spawns = %d", n)
	}
}

func TestBoardLogsAFailedWatcherOnceAndKeepsGoing(t *testing.T) {
	sweepFast(t)
	a := newHarness(t)
	a.ok("init")
	b := siblingFleet(t, a)
	withSupervisor(t, a, false)
	withSupervisor(t, b, false)
	if err := os.Mkdir(filepath.Join(a.home, "watch.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	enableWatcher(t, a.home)
	enableWatcher(t, b.home)
	_, stop := startBoard(t, a, "127.0.0.1")
	eventually(t, func() bool { return watchSpawns(b.home) == 1 })
	settle()
	out := stop()
	if n := strings.Count(out, "watcher "); n != 1 {
		t.Fatalf("board logged %d watcher errors, want 1:\n%s", n, out)
	}
}

func TestBoardStopsKeepingWatchersWhenCancelled(t *testing.T) {
	sweepFast(t)
	h := newHarness(t)
	h.ok("init")
	withSupervisor(t, h, false)
	enableWatcher(t, h.home)
	_, stop := startBoard(t, h, "127.0.0.1")
	eventually(t, func() bool { return watchSpawns(h.home) == 1 })
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("board did not stop after cancel")
	}
}
