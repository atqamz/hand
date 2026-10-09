//go:build unix

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
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func newRun(t *testing.T) *fixture {
	t.Helper()
	f := newCycle(t)
	f.status = "idle"
	uhp := f.uhp
	uhp.Handle("agent.explain", func(raw json.RawMessage) (any, error) {
		f.note("explain")
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status == "error" {
			return nil, errors.New("no such pane")
		}
		return map[string]any{"pane": "2", "agent": "claude", "status": f.status}, nil
	})
	uhp.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"server_generation": uhp.Generation(), "terminals": f.terms}, nil
	})
	return f
}

func byName(fleets []FleetResult) []FleetResult {
	out := slices.Clone(fleets)
	slices.SortFunc(out, func(a, b FleetResult) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func TestRunUpdatesTheFleet(t *testing.T) {
	f := newRun(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "updated" || rep.Failed || len(rep.Backups) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if b, _ := os.ReadFile(f.target); string(b) != handScript(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4") {
		t.Fatalf("target = %q", b)
	}
	for _, p := range rep.Backups {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.log(t, changes...); !slices.Equal(got, []string{"hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if got := byName(rep.Fleets); !slices.Equal(got, []FleetResult{{"alpha", "ok", "kept"}, {f.goneID, "skipped: missing", "kept"}}) {
		t.Fatalf("fleets = %+v", got)
	}
}

func TestRunNamesTheTargetWhenTheSwapFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a 0555 folder")
	}
	f := newRun(t)
	dir := filepath.Dir(f.target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), f.target) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(f.target); string(b) != f.old {
		t.Fatal("target changed")
	}
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func (f *fixture) store(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(f.alpha, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func (f *fixture) supervisor(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	st := f.store(t)
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}, Session: "0f8fad5b-d9cb-469f-a165-70867728950e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	f.terms = append(f.terms, luvus.Terminal{TerminalID: "t1", PaneID: "2", Root: luvus.Root{PID: 1, StartMarker: "m"}, CWD: f.alpha, Label: "hand-supervisor"})
}

func self(t *testing.T) luvus.Root {
	t.Helper()
	m, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return luvus.Root{PID: os.Getpid(), StartMarker: m}
}

func (f *fixture) initialShell(t *testing.T) luvus.Terminal {
	t.Helper()
	dir := filepath.Join(f.alpha, "luvus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return luvus.Terminal{TerminalID: "t0", PaneID: "1", Root: self(t), CWD: dir}
}

func (f *fixture) worker(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	st := f.store(t)
	if _, err := st.AddProject(ctx, "hand", "/home/me/hand"); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "3", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSwitchesAQuietFleet(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.serverRuns(t)
	f.watcherRuns(t)
	f.supervisor(t)
	f.terms = append(f.terms, luvus.Terminal{TerminalID: "t7", PaneID: "9", Root: luvus.Root{PID: 1, StartMarker: "gone"}, CWD: f.alpha, Label: "hand-a3"})
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kill",
		"explain",
		"hand supervisor stop @ " + f.alpha,
		"server.stop",
		"hand supervisor resume @ " + f.alpha,
		"watcher @ " + f.alpha,
	}
	if got := f.log(t, "kill", "force", "watcher @ ", "explain", "supervisor", "server.stop"); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if got := alphaLuvus(t, rep); got != "switched" {
		t.Fatalf("luvus = %q", got)
	}
}

func TestRunLeavesABusyFleetPending(t *testing.T) {
	for name, busy := range map[string]func(*testing.T, *fixture){
		"worker":  func(t *testing.T, f *fixture) { f.worker(t) },
		"working": func(t *testing.T, f *fixture) { f.supervisor(t); f.status = "working" },
		"input": func(t *testing.T, f *fixture) {
			if _, err := f.store(t).AddSupervisorInput(context.Background(), "hello"); err != nil {
				t.Fatal(err)
			}
		},
		"stale pane": func(t *testing.T, f *fixture) { f.supervisor(t); f.uhp.SetGeneration("gen-2") },
		"no inventory": func(t *testing.T, f *fixture) {
			f.uhp.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) { return nil, errors.New("down") })
		},
	} {
		f := newRun(t)
		f.stale()
		busy(t, f)
		rep, err := Run(context.Background(), f.o)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.log(t, "server.stop", "supervisor"); len(got) != 0 || alphaLuvus(t, rep) != "pending" || !slices.Contains(rep.Help, pendingLine) {
			t.Fatalf("%s: calls %q, report %+v", name, got, rep)
		}
	}
}

func TestRunSwitchesBesideLuvusInitialShell(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.serverRuns(t)
	f.terms = []luvus.Terminal{f.initialShell(t)}
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "switched" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunNamesAPaneHandDidNotStart(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.supervisor(t)
	shell := f.initialShell(t)
	other := shell
	other.TerminalID, other.PaneID = "t9", "13"
	f.terms = append(f.terms, shell, other)
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "pending" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	line := "Pane 13 (" + other.CWD + ") is not Hand's, and stopping the Luvus server ends it. " + pendingLine
	if got := f.log(t, "server.stop", "supervisor"); len(got) != 0 || !slices.Contains(rep.Help, line) {
		t.Fatalf("calls %q, help %q", got, rep.Help)
	}
}

func TestRunRetriesAPendingSwitch(t *testing.T) {
	f := newRun(t)
	f.current()
	f.stale()
	f.serverRuns(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "updated" || alphaLuvus(t, rep) != "switched" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, "server.stop", "force"); !slices.Equal(got, []string{"server.stop"}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunKeepsAFleetWhoseServerIsNotRunning(t *testing.T) {
	f := newRun(t)
	f.current()
	f.o.Client = func(fleet.Entry) luvus.Client { return luvus.Client{Socket: filepath.Join(t.TempDir(), "absent.sock")} }
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.o.From = Build{Version: "0.8.0", Channel: "source", Commit: "unknown", Schema: 9, Luvus: "0.14.3"}
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "not running" || len(f.log(t, "server.stop")) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunNeverSwitchesAnUnknownServer(t *testing.T) {
	f := newRun(t)
	f.serverRuns(t)
	f.uhp.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"type": "pong"}, nil })
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "unknown" || len(f.log(t, "server.stop", "force")) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "does not report its version") }) {
		t.Fatalf("help = %q", rep.Help)
	}
	f.current()
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunResumesTheSupervisorWhenTheLuvusStopFails(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.serverRuns(t)
	f.uhp.Handle("server.stop", func(json.RawMessage) (any, error) {
		f.note("server.stop")
		return nil, fakeuhp.Fail{Code: "busy", Message: "no"}
	})
	f.supervisor(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "supervisor"); !slices.Equal(got, []string{"hand supervisor stop @ " + f.alpha, "hand supervisor resume @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if !rep.Failed || !strings.HasPrefix(alphaLuvus(t, rep), "failed: ") {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRunHoldsSignalsBeforeChangingAnything(t *testing.T) {
	f := newRun(t)
	held := 0
	f.o.Hold = func() {
		held++
		f.unchanged(t)
	}
	if _, err := Run(context.Background(), f.o); err != nil || held != 1 {
		t.Fatalf("held %d times, %v", held, err)
	}
	for _, set := range []func(*fixture){func(g *fixture) { g.o.Check = true }, (*fixture).current} {
		g := newRun(t)
		set(g)
		g.o.Hold = func() { held++ }
		if _, err := Run(context.Background(), g.o); err != nil || held != 1 {
			t.Fatalf("held %d times without a change, %v", held, err)
		}
	}
}

func TestRunLeavesTheFleetItRunsInsidePending(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.setenv("LUVUS_SESSION=" + fleet.Session(f.alphaID))
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "server.stop", "supervisor"); len(got) != 0 || alphaLuvus(t, rep) != "pending" || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "runs inside the Luvus session of alpha") }) {
		t.Fatalf("calls %q, report %+v", got, rep)
	}
}

func TestRunLeavesTheServerAloneWithoutAPin(t *testing.T) {
	f := newRun(t)
	if err := os.Remove(filepath.Join(f.root, "luvus", "pin.json")); err != nil {
		t.Fatal(err)
	}
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "dev") {
		f.srv.set(name, body)
	}
	f.current()
	f.stale()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if slices.ContainsFunc(f.srv.asked(), func(p string) bool { return strings.Contains(p, "luvus-") }) {
		t.Fatalf("asked %q", f.srv.asked())
	}
}

func TestRunPrunesOnlyAfterTheSwap(t *testing.T) {
	f := newRun(t)
	old := []string{"20200101T000000", "20200102T000000"}
	for _, st := range old {
		for _, p := range []string{filepath.Join(f.root, "backups", "hand."+st), filepath.Join(f.root, "backups", f.alphaID, "hand.db."+st)} {
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(f.root, "backups", f.alphaID)); !slices.Equal(got, []string{"hand.db.20200102T000000", "hand.db.20261002T010203"}) {
		t.Fatalf("fleet backups = %q", got)
	}
}

func TestRunDoesNotNeedTheSystemTempFolder(t *testing.T) {
	f := newRun(t)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunCheckShowsTheFleetPlan(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "would switch" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	busy := newRun(t)
	busy.stale()
	busy.worker(t)
	busy.o.Check = true
	if rep, err := Run(context.Background(), busy.o); err != nil || alphaLuvus(t, rep) != "would stay pending" {
		t.Fatalf("busy report = %+v, %v", rep, err)
	}
}

func TestRunTreatsAnUnreadableSupervisorAsBusy(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.supervisor(t)
	f.status = "error"
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "pending" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "server.stop", "supervisor"); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunTriesAResumeWhenTheSupervisorStopFails(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.supervisor(t)
	f.o.Env = append(f.o.Env, "HAND_FAIL=supervisor stop")
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "supervisor", "server.stop"); !slices.Equal(got, []string{"hand supervisor stop @ " + f.alpha, "hand supervisor resume @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if !rep.Failed || !strings.HasPrefix(alphaLuvus(t, rep), "failed: ") {
		t.Fatalf("report = %+v", rep)
	}
}

func (f *fixture) stoppedSupervisor(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	st := f.store(t)
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}, Session: "0f8fad5b-d9cb-469f-a165-70867728950e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndSupervisor(ctx, sup.ID, state.AttemptStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
}

func TestRunLeavesNoJournal(t *testing.T) {
	f := newRun(t)
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunFinishesAnInterruptedUpdate(t *testing.T) {
	f := newRun(t)
	f.current()
	f.stoppedSupervisor(t)
	f.leaveJournal(t, true)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "repaired" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{
		"watcher @ " + f.alpha,
		"hand supervisor resume @ " + f.alpha,
		"board @ " + f.alpha,
		"hand init @ " + f.alpha,
	}
	if got := f.log(t, "watcher @ ", "supervisor", "board @ ", "hand init"); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	absent(t, filepath.Join(f.root, "update.json"))
	if b, _ := os.ReadFile(f.target); string(b) != f.old {
		t.Fatal("a repair replaced the binary")
	}
}

func TestRepairRestartsATransientWatcherThroughEnsure(t *testing.T) {
	f := newRun(t)
	f.current()
	b, _ := json.Marshal(map[string][]string{"watcher": {f.alpha}, "init": {f.alpha}})
	if err := os.WriteFile(filepath.Join(f.root, "update.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "repaired" || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, append(changes, "watcher @ ")...); !slices.Equal(got, []string{"watcher @ " + f.alpha, "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRepairLeavesALiveSupervisorAlone(t *testing.T) {
	f := newRun(t)
	f.current()
	f.supervisor(t)
	f.leaveJournal(t, true)
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "supervisor"); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunSetsAsideAnUnreadableJournal(t *testing.T) {
	f := newRun(t)
	if err := os.WriteFile(filepath.Join(f.root, "update.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "updated" || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "could not read") }) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunTreatsAnEmptyJournalAsDone(t *testing.T) {
	f := newRun(t)
	f.current()
	if err := os.WriteFile(filepath.Join(f.root, "update.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunLeavesNoJournalWhenTheBackupFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a 0555 folder")
	}
	f := newRun(t)
	backups := filepath.Join(f.root, "backups")
	if err := os.MkdirAll(backups, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backups, 0o755) })
	if _, err := Run(context.Background(), f.o); err == nil {
		t.Fatal("a failed backup succeeded")
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestUpdateLeavesInstalledUnitsToTheEnsurePath(t *testing.T) {
	f := newRun(t)
	f.watcherRuns(t)
	f.boardRuns(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"kill", "watcher @ " + f.alpha, "kill", "board @ " + f.alpha}
	if got := f.log(t, "kill", "watcher @ ", "board @ "); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunDefaultsTheClientToTheFleetSocket(t *testing.T) {
	f := newRun(t)
	f.stale()
	f.o.Client = nil
	f.setenv("HAND_LUVUS_SOCKET=" + f.uhp.Socket)
	f.o.Check = true
	if rep, err := Run(context.Background(), f.o); err != nil || alphaLuvus(t, rep) != "would switch" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestCheckPlansNothingForAnUnknownOrAbsentServer(t *testing.T) {
	for want, serve := range map[string]func(*fixture){
		"unknown": func(f *fixture) {
			f.uhp.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
		},
		"not running": func(f *fixture) {
			f.o.Client = func(fleet.Entry) luvus.Client { return luvus.Client{Socket: filepath.Join(t.TempDir(), "absent.sock")} }
		},
	} {
		f := newRun(t)
		serve(f)
		f.o.Check = true
		rep, err := Run(context.Background(), f.o)
		if err != nil || alphaLuvus(t, rep) != want || slices.ContainsFunc(rep.Units, func(u UnitResult) bool { return strings.HasPrefix(u.Name, "luvus ") }) {
			t.Fatalf("%s: report = %+v, %v", want, rep, err)
		}
	}
}
