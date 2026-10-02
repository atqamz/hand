package update

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

type fixture struct {
	root, target, calls, sysdir  string
	alpha, alphaID, goneID, gone string
	srv                          *server
	pin                          luvus.Pin
	o                            Options
	mu                           sync.Mutex
	status                       string
}

var oldHand = handScript("0.8.0", "source", "unknown", "7", "0.14.3")

func lookup(env []string) func(string) string {
	return func(k string) string {
		v := ""
		for _, kv := range env {
			if s, ok := strings.CutPrefix(kv, k+"="); ok {
				v = s
			}
		}
		return v
	}
}

func keepLuvus(t *testing.T, root, version string) luvus.Pin {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "luvus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'luvus "+version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := luvus.Keep(context.Background(), root, bin, os.Environ(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

func newRun(t *testing.T, fail map[string]bool) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir()}
	f.alphaID, f.alpha = newFleet(t, f.root, "alpha")
	f.goneID, f.gone = newFleet(t, f.root, "gone")
	if err := os.RemoveAll(f.gone); err != nil {
		t.Fatal(err)
	}
	f.target = binary(t, oldHand)
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "0.14.4")
	maps.Copy(files, fakeLuvus(t, "0.14.4"))
	f.srv = release(t, files)
	show := unitBlock("secondhand-board.service", "active", f.target, f.target+" board --addr 127.0.0.1:7777", "") + "\n" +
		unitBlock("secondhand-watch-alpha.service", "active", f.target, f.target+" watch", "HAND_HOME="+f.alpha) + "\n" +
		unitBlock("secondhand-watch-gone.service", "active", f.target, f.target+" watch", "HAND_HOME="+f.gone)
	f.sysdir, f.calls = fakeSystemctl(t, listing("secondhand-board.service", "secondhand-watch-alpha.service", "secondhand-watch-gone.service"), show, fail)
	f.pin = keepLuvus(t, f.root, "0.14.4")
	f.status = "idle"
	uhp := fakeuhp.Start(t, filepath.Join(t.TempDir(), "uhp.sock"))
	uhp.Handle("agent.explain", func(raw json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if file, err := os.OpenFile(f.calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = file.WriteString("explain\n")
			_ = file.Close()
		}
		if f.status == "error" {
			return nil, errors.New("no such pane")
		}
		return map[string]any{"pane": "2", "agent": "claude", "status": f.status}, nil
	})
	env := append(pathEnv(f.sysdir), "HAND_CALLS="+f.calls, "HAND_LUVUS_SOCKET="+uhp.Socket)
	f.o = Options{
		Target:    f.target,
		From:      Build{Version: "0.8.0", Channel: "source", Commit: "unknown", Schema: 7, Luvus: "0.14.3"},
		Channel:   "edge",
		Root:      f.root,
		HandBase:  f.srv.URL,
		LuvusBase: f.srv.URL,
		Arch:      "amd64",
		Env:       env,
		Getenv:    lookup(env),
		Now:       func() time.Time { return stamp },
		Server: func(context.Context, string) (luvus.Server, bool) {
			return luvus.Server{Exe: f.pin.Path, SHA256: f.pin.SHA256}, true
		},
	}
	return f
}

func (f *fixture) log(t *testing.T, keep ...string) []string {
	t.Helper()
	b, _ := os.ReadFile(f.calls)
	var out []string
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		for _, k := range keep {
			if strings.Contains(line, k) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

func (f *fixture) unchanged(t *testing.T) {
	t.Helper()
	if b, err := os.ReadFile(f.target); err != nil || string(b) != oldHand {
		t.Fatalf("target changed: %v", err)
	}
	absent(t, filepath.Join(f.root, "backups"))
}

var changes = []string{" stop ", " start ", " restart ", "hand "}

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
	if b, _ := os.ReadFile(f.target); string(b) != handScript("0.9.0", "edge", "0123456789ab", "7", "0.14.4") {
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

func TestRunCheckChangesNothing(t *testing.T) {
	f := newRun(t, nil)
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "checked" || rep.To.Commit != "0123456789ab" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunUpToDateChangesNothing(t *testing.T) {
	f := newRun(t, nil)
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 7, Luvus: "0.14.4"}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunRefusesAnOlderSchema(t *testing.T) {
	f := newRun(t, nil)
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "6", "0.14.4") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "update: 0.9.0 reads state schema 6, older than this fleet's 7") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunChangesNothingOnABadChecksum(t *testing.T) {
	f := newRun(t, nil)
	f.srv.set("checksums.txt", []byte(strings.Repeat("0", 64)+"  hand-linux-amd64.tar.gz\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
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
	if b, _ := os.ReadFile(f.target); string(b) != oldHand {
		t.Fatal("target changed")
	}
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestTargetResolvesASymlink(t *testing.T) {
	f := newRun(t, nil)
	link := filepath.Join(t.TempDir(), "hand-next")
	if err := os.Symlink(f.target, link); err != nil {
		t.Fatal(err)
	}
	got, err := Target(link)
	if err != nil || got != f.target {
		t.Fatalf("Target = %q, %v", got, err)
	}
	f.o.Target = got
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if dest, err := os.Readlink(link); err != nil || dest != f.target {
		t.Fatalf("link = %q, %v", dest, err)
	}
	if b, _ := os.ReadFile(link); string(b) != handScript("0.9.0", "edge", "0123456789ab", "7", "0.14.4") {
		t.Fatal("the link does not reach the new binary")
	}
}

func TestRenderPrintsTheReport(t *testing.T) {
	r := Report{
		Status:  "updated",
		From:    Build{Version: "0.8.0", Channel: "source", Commit: "unknown"},
		To:      Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab"},
		PinFrom: "0.14.3",
		PinTo:   "0.14.4",
		Backups: []string{"/r/backups/hand.20261002T010203"},
		Units:   []UnitResult{{"secondhand-board.service", "restart", "ok"}},
		Fleets:  []FleetResult{{"alpha", "ok", "switched"}},
		Help:    []string{"systemctl --user start secondhand-watch-alpha.service"},
	}
	out := Render(r).String()
	for _, want := range []string{"status: updated\n", "from: 0.8.0 source unknown\n", "to: 0.9.0 edge 0123456789ab\n", "luvus: 0.14.3 -> 0.14.4\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	order := []string{"backups[1]:", "units[1]{name,action,result}:\n  secondhand-board.service,restart,ok\n", "fleets[1]{name,init,luvus}:\n  alpha,ok,switched\n", "help[1]:\n  - systemctl --user start secondhand-watch-alpha.service\n"}
	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after %d:\n%s", want, at, out)
		}
		at += i + len(want)
	}
	if at != len(out) {
		t.Fatalf("help is not last:\n%s", out)
	}
	r.PinFrom = "0.14.4"
	if out := Render(r).String(); !strings.Contains(out, "luvus: 0.14.4 kept\n") {
		t.Fatalf("kept:\n%s", out)
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
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 7, Luvus: "0.14.4"}
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

func TestRunPinsTheTestedLuvus(t *testing.T) {
	f := newRun(t, nil)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if pin, ok, err := luvus.LoadPin(f.root); err != nil || !ok || pin.Version != "0.14.4" {
		t.Fatalf("pin = %+v, %v, %v", pin, ok, err)
	}
	if out := Render(rep).String(); !strings.Contains(out, "luvus: 0.14.3 -> 0.14.4\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunNeverLowersThePin(t *testing.T) {
	f := newRun(t, nil)
	f.pin = keepLuvus(t, f.root, "0.15.0")
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if pin, _, _ := luvus.LoadPin(f.root); pin.Version != "0.15.0" {
		t.Fatalf("pin = %+v", pin)
	}
	if slices.ContainsFunc(f.srv.asked(), func(p string) bool { return strings.Contains(p, "luvus-") }) {
		t.Fatalf("asked %q", f.srv.asked())
	}
}

func TestRunSwitchesAQuietFleet(t *testing.T) {
	f := newRun(t, nil)
	f.stale()
	f.supervisor(t)
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
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "dev") {
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

func TestRunRefusesABuildFromAnotherChannel(t *testing.T) {
	f := newRun(t, nil)
	for name, body := range fakeHand(t, "0.9.0", "stable", "0123456789ab", "7", "0.14.4") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "update: the download is a stable build, not edge") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunDoesNotHoldWhileDownloading(t *testing.T) {
	f := newRun(t, nil)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	f.srv.set("luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz", nil)
	held := 0
	f.o.Hold = func() { held++ }
	if _, err := Run(context.Background(), f.o); err == nil || held != 0 {
		t.Fatalf("held %d times, %v", held, err)
	}
	f.unchanged(t)
}

func TestRunKeepsItsChildrenOutOfTheTerminalGroup(t *testing.T) {
	f := newRun(t, nil)
	pgid := filepath.Join(t.TempDir(), "pgid")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'version: 0.9.0\\nchannel: edge\\ncommit: 0123456789ab\\nschema: 7\\nluvus: 0.14.4\\n'; exit 0; fi\nps -o pgid= -p $$ | tr -d ' ' >> " + pgid + "\n"
	archive := tarball(t, "hand", script)
	f.srv.set("hand-linux-amd64.tar.gz", archive)
	f.srv.set("checksums.txt", []byte(sum(archive)+"  hand-linux-amd64.tar.gz\n"))
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pgid)
	if err != nil {
		t.Fatal(err)
	}
	own := strconv.Itoa(syscall.Getpgrp())
	for line := range strings.Lines(string(b)) {
		if strings.TrimSpace(line) == own {
			t.Fatalf("a child ran in the update's process group %s", own)
		}
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
