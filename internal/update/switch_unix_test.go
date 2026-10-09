//go:build unix

package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func (f *fixture) noteAt(t *testing.T, at time.Time, kind, detail string) {
	t.Helper()
	st, err := state.Open(filepath.Join(f.alpha, "hand.db"), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.NoteLuvus(context.Background(), kind, detail); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) later(d time.Duration) {
	f.o.Now = func() time.Time { return stamp.Add(d) }
}

func (f *fixture) latest(t *testing.T, kind string) (state.Event, bool) {
	t.Helper()
	e, ok, err := f.store(t).LatestEvent(context.Background(), kind)
	if err != nil {
		t.Fatal(err)
	}
	return e, ok
}

func (f *fixture) quietStale(t *testing.T) {
	t.Helper()
	f.current()
	f.stale()
	f.serverRuns(t)
	f.watcherRuns(t)
	f.supervisor(t)
}

func TestCurrentBinaryTouchesOnlyTheStaleFleet(t *testing.T) {
	f := newCycle(t)
	f.quietStale(t)
	f.boardRuns(t)
	_, beta := newFleet(t, f.root, "beta")
	f.running(t, filepath.Join(beta, "watch.pid"), f.target)
	dir, err := os.MkdirTemp("", "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	current := fakeuhp.Start(t, filepath.Join(dir, "s"))
	current.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"version": f.pin.Version}, nil })
	f.o.Client = func(e fleet.Entry) luvus.Client {
		if e.Name == "beta" {
			return luvus.Client{Socket: current.Socket}
		}
		return luvus.Client{Socket: f.uhp.Socket}
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "kill", "watcher @ ", "board @ ", "hand "); !slices.Equal(got, []string{"kill", "hand supervisor stop @ " + f.alpha, "hand supervisor resume @ " + f.alpha, "watcher @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if len(rep.Fleets) != 1 || rep.Fleets[0] != (FleetResult{"alpha", "skipped: current", "switched"}) {
		t.Fatalf("fleets = %+v", rep.Fleets)
	}
	for _, p := range []string{filepath.Join(beta, "watch.pid"), filepath.Join(f.root, "board.pid")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("a fleet that needs nothing lost %s: %v", p, err)
		}
	}
	f.unchanged(t)
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestBusyStaleFleetChangesNothing(t *testing.T) {
	for name, busy := range busyFleets {
		f := newCycle(t)
		f.current()
		f.stale()
		f.watcherRuns(t)
		f.boardRuns(t)
		busy(t, f)
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Failed || rep.Status != "up to date" || !strings.HasPrefix(alphaLuvus(t, rep), "pending: ") || !slices.Contains(rep.Help, pendingLine) {
			t.Fatalf("%s: report = %+v, %v", name, rep, err)
		}
		if got := f.log(t, changes...); len(got) != 0 {
			t.Fatalf("%s: calls = %q", name, got)
		}
		if len(f.slept) != 0 {
			t.Fatalf("%s: waited %v", name, f.slept)
		}
		for _, p := range []string{filepath.Join(f.alpha, "watch.pid"), filepath.Join(f.root, "board.pid")} {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s: lost %s: %v", name, p, err)
			}
		}
		absent(t, filepath.Join(f.root, "update.json"))
		f.unchanged(t)
	}
}

func TestRecentEventKeepsTheFleetPending(t *testing.T) {
	for age, want := range map[time.Duration]string{
		time.Minute:      "pending: the fleet recorded an event within the last 15 minutes",
		14 * time.Minute: "pending: the fleet recorded an event within the last 15 minutes",
		16 * time.Minute: "switched",
	} {
		f := newCycle(t)
		f.quietStale(t)
		f.noteAt(t, stamp.Add(-age), "switched", "0.0.1 -> 0.0.2")
		rep, err := Run(context.Background(), f.o)
		if err != nil || alphaLuvus(t, rep) != want {
			t.Fatalf("%v old: report = %+v, %v", age, rep, err)
		}
		if got := f.log(t, "kill", "server.stop", "supervisor"); (want == "switched") != (len(got) > 0) {
			t.Fatalf("%v old: calls = %q", age, got)
		}
	}
}

func TestTypingInTheSupervisorPaneKeepsItPending(t *testing.T) {
	f := newCycle(t)
	f.quietStale(t)
	f.typed = 1
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || alphaLuvus(t, rep) != "pending: the supervisor's screen changed" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "kill", "supervisor", "server.stop", "luvus start", "watcher @ "); !slices.Equal(got, []string{"kill", "watcher @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if !slices.Equal(f.slept, []time.Duration{30 * time.Second}) {
		t.Fatalf("waited %v", f.slept)
	}
	if _, ok := f.latest(t, "luvus.failed"); ok {
		t.Fatal("a screen that moved recorded a failure")
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestAnEventDuringTheWaitKeepsItPending(t *testing.T) {
	f := newCycle(t)
	f.quietStale(t)
	sleep := f.o.Sleep
	f.o.Sleep = func(ctx context.Context, d time.Duration) error {
		f.noteAt(t, stamp, "switched", "0.0.1 -> 0.0.2")
		return sleep(ctx, d)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || alphaLuvus(t, rep) != "pending: the fleet recorded an event within the last 15 minutes" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "kill", "supervisor", "server.stop", "luvus start", "watcher @ "); !slices.Equal(got, []string{"kill", "watcher @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestQueuedInputAfterTheSupervisorStopResumesAndStaysPending(t *testing.T) {
	f := newCycle(t)
	f.quietStale(t)
	f.setenv("HAND_QUEUE_ON_STOP=1")
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || alphaLuvus(t, rep) != "pending: a message waits for the supervisor" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"kill", "hand supervisor stop @ " + f.alpha, "hand supervisor resume @ " + f.alpha, "watcher @ " + f.alpha}
	if got := f.log(t, "kill", "supervisor", "server.stop", "luvus start", "watcher @ "); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if _, ok := f.latest(t, "luvus.failed"); ok {
		t.Fatal("a queued message recorded a failure")
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestServerBackWithTheOldVersionFailsOnce(t *testing.T) {
	f := newCycle(t)
	f.quietStale(t)
	f.startsOld = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !rep.Failed || alphaLuvus(t, rep) != `failed: the server came back as "0.0.1", not the pinned 0.14.4` {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"kill", "hand supervisor stop @ " + f.alpha, "server.stop", "luvus start", "hand supervisor resume @ " + f.alpha, "watcher @ " + f.alpha}
	if got := f.log(t, "kill", "supervisor", "server.stop", "luvus start", "watcher @ "); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if e, ok := f.latest(t, "luvus.failed"); !ok || e.Detail != `0.14.4: the server came back as "0.0.1", not the pinned 0.14.4` {
		t.Fatalf("event = %+v, %v", e, ok)
	}
	if _, ok := f.latest(t, "luvus.switched"); ok {
		t.Fatal("a failed switch recorded a success")
	}
	f.later(time.Hour)
	f.o.Auto = true
	f.watcherRuns(t)
	rep, err = Run(context.Background(), f.o)
	if err != nil || !strings.HasPrefix(alphaLuvus(t, rep), "pending: an earlier switch to 0.14.4 failed") {
		t.Fatalf("second report = %+v, %v", rep, err)
	}
	if got := f.log(t, "server.stop"); len(got) != 1 {
		t.Fatalf("server.stop calls = %q", got)
	}
}

func TestFailedSwitchIsNotRetriedForTheSamePin(t *testing.T) {
	for name, tc := range map[string]struct {
		auto   bool
		failed string
		want   string
	}{
		"timer, same pin":      {true, "0.14.4: no server", "pending: an earlier switch to 0.14.4 failed; run `hand update` to try again"},
		"timer, another pin":   {true, "0.14.3: no server", "switched"},
		"manual run, same pin": {false, "0.14.4: no server", "switched"},
	} {
		f := newCycle(t)
		f.quietStale(t)
		f.noteAt(t, stamp.Add(-time.Hour), "failed", tc.failed)
		f.o.Auto = tc.auto
		rep, err := Run(context.Background(), f.o)
		if err != nil || alphaLuvus(t, rep) != tc.want {
			t.Fatalf("%s: report = %+v, %v", name, rep, err)
		}
		if got := f.log(t, "server.stop"); (tc.want == "switched") != (len(got) == 1) {
			t.Fatalf("%s: server.stop calls = %q", name, got)
		}
	}
}

func TestKeepLuvusNeverSwitches(t *testing.T) {
	for name, current := range map[string]bool{"current binary": true, "new binary": false} {
		f := newCycle(t)
		f.quietStale(t)
		if !current {
			f.o.From = Build{Version: "0.8.0", Channel: "source", Commit: "unknown", Schema: 9, Luvus: "0.14.3"}
		}
		f.o.KeepLuvus = true
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Failed {
			t.Fatalf("%s: report = %+v, %v", name, rep, err)
		}
		if got := f.log(t, "server.stop", "supervisor", "luvus start"); len(got) != 0 {
			t.Fatalf("%s: calls = %q", name, got)
		}
		if current && len(rep.Fleets) != 0 || !current && alphaLuvus(t, rep) != "pending: --keep-luvus" {
			t.Fatalf("%s: fleets = %+v", name, rep.Fleets)
		}
	}
}
