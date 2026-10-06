//go:build unix

package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

func newRun(t *testing.T, fail map[string]bool) *fixture {
	t.Helper()
	f := newFetch(t)
	f.alphaID, f.alpha = newFleet(t, f.root, "alpha")
	f.goneID, f.gone = newFleet(t, f.root, "gone")
	if err := os.RemoveAll(f.gone); err != nil {
		t.Fatal(err)
	}
	show := unitBlock("secondhand-board.service", "active", f.target, f.target+" board --addr 127.0.0.1:7777", "") + "\n" +
		unitBlock("secondhand-watch-alpha.service", "active", f.target, f.target+" watch", "HAND_HOME="+f.alpha) + "\n" +
		unitBlock("secondhand-watch-gone.service", "active", f.target, f.target+" watch", "HAND_HOME="+f.gone)
	f.sysdir, f.calls = fakeSystemctl(t, listing("secondhand-board.service", "secondhand-watch-alpha.service", "secondhand-watch-gone.service"), show, fail)
	f.status = "idle"
	uhp := fakeuhp.Start(t, filepath.Join(t.TempDir(), "uhp.sock"))
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
	f.uhp = uhp
	env := append(pathEnv(f.sysdir), "HAND_CALLS="+f.calls, "HAND_LUVUS_SOCKET="+uhp.Socket)
	f.o.Env, f.o.Getenv = env, lookup(env)
	f.o.Watch = func(_ context.Context, home string) error {
		f.note("watcher @ " + home)
		return nil
	}
	return f
}

func (f *fixture) note(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if file, err := os.OpenFile(f.calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = file.WriteString(line + "\n")
		_ = file.Close()
	}
}

func (f *fixture) watchTransient(t *testing.T, value string) {
	t.Helper()
	path := filepath.Join(f.sysdir, "show.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	id := "Id=secondhand-watch-alpha.service\n"
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), id, id+"Transient="+value+"\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func byName(fleets []FleetResult) []FleetResult {
	out := slices.Clone(fleets)
	slices.SortFunc(out, func(a, b FleetResult) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func TestRunUpdatesTheFleet(t *testing.T) {
	f := newRun(t, nil)
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
	want := []string{"systemctl --user stop secondhand-watch-alpha.service", "systemctl --user start secondhand-watch-alpha.service", "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}
	if got := f.log(t, changes...); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if got := byName(rep.Fleets); !slices.Equal(got, []FleetResult{{"alpha", "ok", "kept"}, {f.goneID, "skipped: missing", "kept"}}) {
		t.Fatalf("fleets = %+v", got)
	}
}

func TestRunReportsAFailedUnitAndCarriesOn(t *testing.T) {
	f := newRun(t, map[string]bool{"start": true})
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed || !slices.ContainsFunc(rep.Help, func(h string) bool {
		return strings.Contains(h, "systemctl --user start secondhand-watch-alpha.service")
	}) {
		t.Fatalf("report = %+v", rep)
	}
	got := f.log(t, " restart ", "hand init")
	if !slices.Equal(got, []string{"systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunNamesTheTargetWhenTheSwapFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a 0555 folder")
	}
	f := newRun(t, nil)
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

func (f *fixture) stale() {
	f.o.Server = func(context.Context, string) (luvus.Server, bool) {
		return luvus.Server{Exe: "/old/luvus", SHA256: "old"}, true
	}
}

func (f *fixture) current() {
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 9, Luvus: "0.14.4"}
}

func alphaLuvus(t *testing.T, rep Report) string {
	t.Helper()
	for _, fr := range rep.Fleets {
		if fr.Name == "alpha" {
			return fr.Luvus
		}
	}
	t.Fatalf("no alpha in %+v", rep.Fleets)
	return ""
}

func TestRunSwitchesAQuietFleet(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.supervisor(t)
	f.terms = append(f.terms, luvus.Terminal{TerminalID: "t7", PaneID: "9", Root: luvus.Root{PID: 1, StartMarker: "gone"}, CWD: f.alpha, Label: "hand-a3"})
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user stop secondhand-watch-alpha.service",
		"explain",
		"hand supervisor stop @ " + f.alpha,
		"systemctl --user stop " + fleet.LuvusUnit(f.alphaID) + ".service",
		"hand supervisor resume @ " + f.alpha,
		"systemctl --user start secondhand-watch-alpha.service",
	}
	if got := f.log(t, " stop secondhand-watch-alpha", " start secondhand-watch-alpha", "explain", "supervisor", "secondhand-luvus-"); !slices.Equal(got, want) {
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
		f := newRun(t, nil)
		f.stale()
		busy(t, f)
		rep, err := Run(context.Background(), f.o)
		if err != nil {
			t.Fatal(err)
		}
		line := "At a quiet time: `systemctl --user stop " + fleet.LuvusUnit(f.alphaID) + ".service`, then `hand supervisor resume` in " + f.alpha
		if got := f.log(t, "secondhand-luvus-", "supervisor"); len(got) != 0 || alphaLuvus(t, rep) != "pending" || !slices.Contains(rep.Help, line) {
			t.Fatalf("%s: calls %q, report %+v", name, got, rep)
		}
	}
}

func TestRunSwitchesBesideLuvusInitialShell(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.terms = []luvus.Terminal{f.initialShell(t)}
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "switched" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunNamesAPaneHandDidNotStart(t *testing.T) {
	f := newRun(t, nil)
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
	line := "Pane 13 (" + other.CWD + ") is not Hand's, and stopping the Luvus server ends it. " + pendingLine(fleet.Entry{ID: f.alphaID, Home: f.alpha})
	if got := f.log(t, "secondhand-luvus-", "supervisor"); len(got) != 0 || !slices.Contains(rep.Help, line) {
		t.Fatalf("calls %q, help %q", got, rep.Help)
	}
}

func TestRunRetriesAPendingSwitch(t *testing.T) {
	f := newRun(t, nil)
	f.current()
	f.stale()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "updated" || alphaLuvus(t, rep) != "switched" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, "secondhand-luvus-"); !slices.Equal(got, []string{"systemctl --user stop " + fleet.LuvusUnit(f.alphaID) + ".service"}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunKeepsAFleetWhoseServerIsNotRunning(t *testing.T) {
	f := newRun(t, nil)
	f.current()
	f.o.Server = func(context.Context, string) (luvus.Server, bool) { return luvus.Server{}, false }
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunDoesNotSwitchWithoutStoppingTheWatcher(t *testing.T) {
	for _, fail := range []string{"stop secondhand-watch-alpha", "list-units"} {
		f := newRun(t, map[string]bool{fail: true})
		f.stale()
		rep, err := Run(context.Background(), f.o)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.log(t, "secondhand-luvus-"); len(got) != 0 || alphaLuvus(t, rep) != "pending" || !rep.Failed {
			t.Fatalf("%s: calls %q, report %+v", fail, got, rep)
		}
	}
}

func TestRunResumesTheSupervisorWhenTheLuvusStopFails(t *testing.T) {
	f := newRun(t, map[string]bool{"stop secondhand-luvus-": true})
	f.stale()
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
	f := newRun(t, nil)
	held := 0
	f.o.Hold = func() {
		held++
		f.unchanged(t)
	}
	if _, err := Run(context.Background(), f.o); err != nil || held != 1 {
		t.Fatalf("held %d times, %v", held, err)
	}
	for _, set := range []func(*fixture){func(g *fixture) { g.o.Check = true }, (*fixture).current} {
		g := newRun(t, nil)
		set(g)
		g.o.Hold = func() { held++ }
		if _, err := Run(context.Background(), g.o); err != nil || held != 1 {
			t.Fatalf("held %d times without a change, %v", held, err)
		}
	}
}

func TestRunLeavesTheFleetItRunsInsidePending(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.o.Cgroup = "0::/user.slice/user-1000.slice/user@1000.service/app.slice/" + fleet.LuvusUnit(f.alphaID) + ".service\n"
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "secondhand-luvus-", "supervisor"); len(got) != 0 || alphaLuvus(t, rep) != "pending" || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "runs inside "+fleet.LuvusUnit(f.alphaID)) }) {
		t.Fatalf("calls %q, report %+v", got, rep)
	}
}

func TestRunLeavesTheServerAloneWithoutAPin(t *testing.T) {
	f := newRun(t, nil)
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
	f := newRun(t, nil)
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
	f := newRun(t, nil)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
}

func TestRunSaysWhenSystemctlIsMissing(t *testing.T) {
	f := newRun(t, nil)
	f.o.Env = []string{"PATH=" + t.TempDir(), "HAND_CALLS=" + f.calls}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "updated" || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "systemctl was not found") }) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "hand init"); !slices.Equal(got, []string{"hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunCheckShowsTheFleetPlan(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "would switch" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	busy := newRun(t, nil)
	busy.stale()
	busy.worker(t)
	busy.o.Check = true
	if rep, err := Run(context.Background(), busy.o); err != nil || alphaLuvus(t, rep) != "would stay pending" {
		t.Fatalf("busy report = %+v, %v", rep, err)
	}
}

func TestRunCheckListsTheUnitActionsTheRealRunTakes(t *testing.T) {
	for name, setup := range map[string]func(*fixture){
		"installed": func(*fixture) {},
		"transient": func(f *fixture) { f.watchTransient(t, "yes") },
		"stale":     func(f *fixture) { f.stale() },
	} {
		t.Run(name, func(t *testing.T) {
			check := newRun(t, nil)
			setup(check)
			check.o.Check = true
			previewed, err := Run(context.Background(), check.o)
			if err != nil || previewed.Failed {
				t.Fatalf("check = %+v, %v", previewed, err)
			}
			check.unchanged(t)
			if got := check.log(t, append(changes, "watcher @ ")...); len(got) != 0 {
				t.Fatalf("check calls = %q", got)
			}
			real := newRun(t, nil)
			setup(real)
			ran, err := Run(context.Background(), real.o)
			if err != nil || ran.Failed {
				t.Fatalf("run = %+v, %v", ran, err)
			}
			if len(previewed.Units) < 3 {
				t.Fatalf("check units = %+v", previewed.Units)
			}
			rows := func(f *fixture, units []UnitResult) []string {
				var out []string
				for _, u := range units {
					name := strings.NewReplacer(f.alpha, "ALPHA", f.alphaID, "ID").Replace(u.Name)
					out = append(out, name+" "+u.Action)
				}
				return out
			}
			for _, u := range previewed.Units {
				if u.Result != "would run" {
					t.Fatalf("check unit = %+v", u)
				}
			}
			if got, want := rows(check, previewed.Units), rows(real, ran.Units); !slices.Equal(got, want) {
				t.Fatalf("check units\n%q\nrun units\n%q", got, want)
			}
		})
	}
}

func TestRunTreatsAnUnreadableSupervisorAsBusy(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.supervisor(t)
	f.status = "error"
	rep, err := Run(context.Background(), f.o)
	if err != nil || alphaLuvus(t, rep) != "pending" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, "secondhand-luvus-", "supervisor"); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunTriesAResumeWhenTheSupervisorStopFails(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.supervisor(t)
	f.o.Env = append(f.o.Env, "HAND_FAIL=supervisor stop")
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.log(t, "supervisor", "secondhand-luvus-"); !slices.Equal(got, []string{"hand supervisor stop @ " + f.alpha, "hand supervisor resume @ " + f.alpha}) {
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

func (f *fixture) leaveJournal(t *testing.T, supervisor bool) {
	t.Helper()
	j := map[string][]string{"watch": {"secondhand-watch-alpha.service"}, "board": {"secondhand-board.service"}, "init": {f.alpha}}
	if supervisor {
		j["supervisor"] = []string{f.alpha}
	}
	b, _ := json.Marshal(j)
	if err := os.WriteFile(filepath.Join(f.root, "update.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunLeavesNoJournal(t *testing.T) {
	f := newRun(t, nil)
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunJournalsItsStepsBeforeTakingThem(t *testing.T) {
	f := newRun(t, nil)
	copyAt := filepath.Join(t.TempDir(), "journal-at-stop")
	script, err := os.ReadFile(filepath.Join(f.sysdir, "systemctl"))
	if err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nif [ \"$2\" = stop ]; then cp " + filepath.Join(f.root, "update.json") + " " + copyAt + "; fi\n"
	if err := os.WriteFile(filepath.Join(f.sysdir, "systemctl"), append([]byte(hook), script[len("#!/bin/sh\n"):]...), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(copyAt)
	if err != nil {
		t.Fatalf("no journal when the watcher stopped: %v", err)
	}
	var j map[string][]string
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(j["watch"], []string{"secondhand-watch-alpha.service"}) || !slices.Equal(j["board"], []string{"secondhand-board.service"}) || !slices.Equal(j["init"], []string{f.alpha}) {
		t.Fatalf("journal at the stop = %s", b)
	}
}

func TestRunFinishesAnInterruptedUpdate(t *testing.T) {
	f := newRun(t, nil)
	f.current()
	f.stoppedSupervisor(t)
	f.leaveJournal(t, true)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "repaired" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{
		"systemctl --user start secondhand-watch-alpha.service",
		"hand supervisor resume @ " + f.alpha,
		"systemctl --user restart secondhand-board.service",
		"hand init @ " + f.alpha,
	}
	if got := f.log(t, " start ", "supervisor", " restart ", "hand init"); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	absent(t, filepath.Join(f.root, "update.json"))
	if b, _ := os.ReadFile(f.target); string(b) != f.old {
		t.Fatal("a repair replaced the binary")
	}
}

func TestUpdateRestartsATransientWatcherThroughEnsure(t *testing.T) {
	f := newRun(t, nil)
	f.watchTransient(t, "yes")
	var journaled []string
	f.o.Watch = func(_ context.Context, home string) error {
		f.note("watcher @ " + home)
		j, _, err := loadJournal(f.root)
		if err != nil {
			t.Error(err)
		}
		journaled = j.Watcher
		return nil
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"systemctl --user stop secondhand-watch-alpha.service", "watcher @ " + f.alpha, "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}
	if got := f.log(t, append(changes, "watcher @ ")...); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if !slices.Equal(journaled, []string{f.alpha}) {
		t.Fatalf("journal at the hook = %q", journaled)
	}
}

func TestUpdateKeepsInstalledWatchUnits(t *testing.T) {
	f := newRun(t, nil)
	f.watchTransient(t, "no")
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl --user stop secondhand-watch-alpha.service", "systemctl --user start secondhand-watch-alpha.service", "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}
	if got := f.log(t, append(changes, "watcher @ ")...); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRepairRestartsATransientWatcherThroughEnsure(t *testing.T) {
	f := newRun(t, nil)
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

func TestRepairKeepsInstalledWatchUnits(t *testing.T) {
	f := newRun(t, nil)
	f.current()
	f.leaveJournal(t, false)
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl --user start secondhand-watch-alpha.service", "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}
	if got := f.log(t, append(changes, "watcher @ ")...); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRepairLeavesALiveSupervisorAlone(t *testing.T) {
	f := newRun(t, nil)
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

func TestCheckReportsAnInterruptedUpdate(t *testing.T) {
	f := newRun(t, nil)
	f.current()
	f.leaveJournal(t, false)
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "stopped part way") }) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "update.json")); err != nil {
		t.Fatalf("check removed the journal: %v", err)
	}
}

func TestRunRepairsBeforeFetching(t *testing.T) {
	f := newRun(t, nil)
	f.leaveJournal(t, false)
	f.srv.set("hand-linux-amd64.tar.gz", nil)
	if _, err := Run(context.Background(), f.o); err == nil {
		t.Fatal("a failed download succeeded")
	}
	if got := f.log(t, " start ", " restart ", "hand init"); !slices.Equal(got, []string{"systemctl --user start secondhand-watch-alpha.service", "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunSetsAsideAnUnreadableJournal(t *testing.T) {
	f := newRun(t, nil)
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
	f := newRun(t, nil)
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
	f := newRun(t, nil)
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

func TestRunStopsPIDFileProcessesInsteadOfUnits(t *testing.T) {
	f := newRun(t, nil)
	self := os.Getpid()
	marker, err := luvus.ProcStartMarker(self)
	if err != nil {
		t.Skip(err)
	}
	entry := strconv.Itoa(self) + " " + marker + "\n"
	if err := os.WriteFile(filepath.Join(f.root, "board.pid"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.alpha, "watch.pid"), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	var stopped []string
	f.o.Stop = func(pid int, m string) error {
		stopped = append(stopped, strconv.Itoa(pid)+" "+m)
		return nil
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if want := []string{strconv.Itoa(self) + " " + marker, strconv.Itoa(self) + " " + marker}; !slices.Equal(stopped, want) {
		t.Fatalf("stopped = %q, want %q", stopped, want)
	}
	if got := f.log(t, " stop ", " start ", " restart "); len(got) != 0 {
		t.Fatalf("systemctl calls = %q", got)
	}
	if !slices.Contains(rep.Help, "Start the board again: `hand board`") {
		t.Fatalf("help = %q", rep.Help)
	}
	if want := "Start the watch again in " + f.alpha + ": `hand watch`; the next `hand supervisor start` or `hand supervisor resume` also starts it"; !slices.Contains(rep.Help, want) {
		t.Fatalf("help = %q, want %q", rep.Help, want)
	}
	absent(t, filepath.Join(f.root, "board.pid"))
	absent(t, filepath.Join(f.alpha, "watch.pid"))
}

func TestRunDoesNotAskToRestartWhatItFailedToStop(t *testing.T) {
	f := newRun(t, nil)
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Skip(err)
	}
	for _, p := range []string{filepath.Join(f.alpha, "watch.pid"), filepath.Join(f.root, "board.pid")} {
		if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+" "+marker+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.o.Stop = func(int, string) error { return errors.New("denied") }
	rep, _ := Run(context.Background(), f.o)
	for _, h := range rep.Help {
		if strings.HasPrefix(h, "Start the watch again") || strings.HasPrefix(h, "Start the board again") {
			t.Fatalf("help = %q", rep.Help)
		}
	}
}
