package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func newCycle(t *testing.T) *fixture {
	t.Helper()
	f := newFetch(t)
	f.alphaID, f.alpha = newFleet(t, f.root, "alpha")
	f.goneID, f.gone = newFleet(t, f.root, "gone")
	if err := os.RemoveAll(f.gone); err != nil {
		t.Fatal(err)
	}
	f.o.Stop = func(int, string) error {
		f.note("kill")
		return f.stopErr
	}
	f.o.Kill = func(int, string) error {
		f.note("force")
		return nil
	}
	dir, err := os.MkdirTemp("", "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f.uhp = fakeuhp.Start(t, filepath.Join(dir, "s"))
	f.version, f.sessionDir = f.pin.Version, t.TempDir()
	f.uhp.Handle("ping", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"type": "pong", "version": f.version}, nil
	})
	f.uhp.Handle("agent.explain", func(raw json.RawMessage) (any, error) {
		f.note("explain")
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status == "error" {
			return nil, errors.New("no such pane")
		}
		return map[string]any{"pane": "2", "agent": "claude", "status": f.status}, nil
	})
	f.uhp.Handle("agent.read", func(json.RawMessage) (any, error) {
		f.note("read")
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"text": "", "content_revision": f.revision, "terminal_id": "t1"}, nil
	})
	f.uhp.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"server_generation": f.uhp.Generation(), "terminals": f.terms}, nil
	})
	f.status = "idle"
	f.o.Client = func(fleet.Entry) luvus.Client { return luvus.Client{Socket: f.uhp.Socket} }
	f.setenv("HAND_SESSION="+fleet.Session(f.alphaID), "HAND_SESSION_DIR="+f.sessionDir)
	f.o.Watch = func(_ context.Context, home string) error {
		f.note("watcher @ " + home)
		return nil
	}
	f.o.Board = func(_ context.Context, home string) error {
		f.note("board @ " + home)
		return nil
	}
	f.o.Start = func(context.Context, string) error {
		f.note("luvus start")
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.startsOld {
			f.version = f.pin.Version
		}
		return nil
	}
	f.o.Sleep = func(_ context.Context, d time.Duration) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.slept = append(f.slept, d)
		f.revision += f.typed
		return nil
	}
	return f
}

func (f *fixture) setenv(kv ...string) {
	env := append(slices.Clone(f.o.Env), kv...)
	f.o.Env, f.o.Getenv = env, lookup(env)
}

func (f *fixture) note(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if file, err := os.OpenFile(f.calls, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = file.WriteString(line + "\n")
		_ = file.Close()
	}
}

func (f *fixture) running(t *testing.T, path, exe string) {
	t.Helper()
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d %s %s\n", os.Getpid(), marker, exe)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) watcherRuns(t *testing.T) {
	t.Helper()
	f.running(t, filepath.Join(f.alpha, "watch.pid"), f.target)
}

func (f *fixture) boardRuns(t *testing.T) {
	t.Helper()
	f.running(t, filepath.Join(f.root, "board.pid"), f.target)
}

func (f *fixture) processCalls(t *testing.T) []string {
	t.Helper()
	return f.log(t, "kill", "watcher @ ", "board @ ", "hand init")
}

func TestUpdateStopsByPIDAndRestartsThroughEnsure(t *testing.T) {
	f := newCycle(t)
	f.watcherRuns(t)
	f.boardRuns(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Status != "updated" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"kill", "watcher @ " + f.alpha, "kill", "board @ " + f.alpha, "hand init @ " + f.alpha}
	if got := f.processCalls(t); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	wantRows := []UnitResult{{"watch alpha", "stop", "ok"}, {"watch " + f.alpha, "start", "ok"}, {"board", "stop", "ok"}, {"board", "start", "ok"}}
	if !slices.Equal(rep.Units, wantRows) {
		t.Fatalf("units = %+v", rep.Units)
	}
	absent(t, filepath.Join(f.alpha, "watch.pid"))
	absent(t, filepath.Join(f.root, "board.pid"))
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestUpdateRestartsOnlyWhatItStopped(t *testing.T) {
	f := newCycle(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || len(rep.Units) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	f = newCycle(t)
	f.boardRuns(t)
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"kill", "board @ " + f.alpha, "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateSkipsAProcessOfAnotherBinary(t *testing.T) {
	f := newCycle(t)
	f.stale()
	other := filepath.Join(t.TempDir(), "other-hand")
	f.running(t, filepath.Join(f.alpha, "watch.pid"), other)
	f.running(t, filepath.Join(f.root, "board.pid"), other)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	for _, u := range rep.Units {
		if u.Action != "stop" || u.Result != "skipped: runs "+other {
			t.Fatalf("unit = %+v", u)
		}
	}
	if len(rep.Units) != 2 || alphaLuvus(t, rep) != "pending: the watcher is still running" || !slices.Contains(rep.Help, "watch alpha runs "+other+", not "+f.target+", so it was left running") {
		t.Fatalf("report = %+v", rep)
	}
	for _, name := range []string{filepath.Join(f.alpha, "watch.pid"), filepath.Join(f.root, "board.pid")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("a skipped process lost its pid file: %v", err)
		}
	}
}

func TestUpdateStopsAProcessWhoseFileHoldsNoBinary(t *testing.T) {
	f := newCycle(t)
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.alpha, "watch.pid"), []byte(fmt.Sprintf("%d %s\n", os.Getpid(), marker)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"kill", "watcher @ " + f.alpha, "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateIgnoresAStalePIDFile(t *testing.T) {
	f := newCycle(t)
	for path, body := range map[string]string{
		filepath.Join(f.alpha, "watch.pid"): fmt.Sprintf("%d recycled %s\n", os.Getpid(), f.target),
		filepath.Join(f.root, "board.pid"):  "not a pid file\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.o.Stop = func(int, string) error {
		t.Error("stopped a process whose start marker does not match")
		return nil
	}
	if _, live := livePID(filepath.Join(f.alpha, "watch.pid")); live {
		t.Fatal("a recycled pid counted as live")
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || len(rep.Units) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateDoesNotRestartWhatItFailedToStop(t *testing.T) {
	f := newCycle(t)
	f.stale()
	f.stopErr = errors.New("still alive after SIGTERM")
	f.watcherRuns(t)
	f.boardRuns(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || !rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.processCalls(t); !slices.Equal(got, []string{"kill", "kill", "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
	if alphaLuvus(t, rep) != "pending: the watcher is still running" || !slices.ContainsFunc(rep.Help, func(h string) bool {
		return strings.HasPrefix(h, "could not stop watch alpha (still alive after SIGTERM)")
	}) {
		t.Fatalf("report = %+v", rep)
	}
	for _, name := range []string{filepath.Join(f.alpha, "watch.pid"), filepath.Join(f.root, "board.pid")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("a process that kept running lost its pid file: %v", err)
		}
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestUpdateReportsAFailedStart(t *testing.T) {
	f := newCycle(t)
	f.watcherRuns(t)
	f.boardRuns(t)
	f.o.Watch = func(context.Context, string) error { return errors.New("no lock") }
	f.o.Board = func(context.Context, string) error { return errors.New("no port") }
	rep, err := Run(context.Background(), f.o)
	if err != nil || !rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	for _, want := range []string{"could not start watch " + f.alpha + " (no lock); run `hand watch` for it", "could not start board (no port); run `hand board` for it"} {
		if !slices.Contains(rep.Help, want) {
			t.Fatalf("help = %q, want %q", rep.Help, want)
		}
	}
	if got := f.log(t, "hand init"); len(got) != 1 {
		t.Fatalf("init calls = %q", got)
	}
}

func TestUpdateJournalsItsStepsBeforeStopping(t *testing.T) {
	f := newCycle(t)
	f.watcherRuns(t)
	f.boardRuns(t)
	var seen []journal
	f.o.Stop = func(int, string) error {
		j, _, err := loadJournal(f.root)
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, j)
		return nil
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !slices.Equal(seen[0].Watcher, []string{f.alpha}) || len(seen[0].Board) != 0 || !slices.Equal(seen[1].Board, []string{f.alpha}) || len(seen[1].Watcher) != 0 || !slices.Equal(seen[1].Init, []string{f.alpha}) {
		t.Fatalf("journals at the stops = %+v", seen)
	}
}

func TestUpdateKeepsRunningWhenItCannotJournal(t *testing.T) {
	f := newCycle(t)
	f.watcherRuns(t)
	rep := Report{}
	o := f.o
	o.Root = filepath.Join(t.TempDir(), "missing")
	rep.cycle(context.Background(), o, entries(t, f.root), true, func(fleet.Entry) string { return "stale" })
	if got := f.processCalls(t); len(got) != 0 || !rep.Failed {
		t.Fatalf("calls %q, report %+v", got, rep)
	}
}

func TestUpdateCheckWalksTheRealRunsPath(t *testing.T) {
	for name, setup := range map[string]func(*fixture){
		"both run":   func(f *fixture) { f.watcherRuns(t); f.boardRuns(t) },
		"board only": func(f *fixture) { f.boardRuns(t) },
		"none":       func(*fixture) {},
		"other binary": func(f *fixture) {
			f.running(t, filepath.Join(f.alpha, "watch.pid"), filepath.Join(t.TempDir(), "other-hand"))
			f.boardRuns(t)
		},
	} {
		t.Run(name, func(t *testing.T) {
			check := newCycle(t)
			setup(check)
			var pids []string
			for _, p := range []string{filepath.Join(check.alpha, "watch.pid"), filepath.Join(check.root, "board.pid")} {
				if _, err := os.Stat(p); err == nil {
					pids = append(pids, p)
				}
			}
			check.o.Check = true
			previewed, err := Run(context.Background(), check.o)
			if err != nil || previewed.Failed || previewed.Status != "checked" {
				t.Fatalf("check = %+v, %v", previewed, err)
			}
			check.unchanged(t)
			if got := check.log(t, changes...); len(got) != 0 {
				t.Fatalf("check calls = %q", got)
			}
			for _, p := range pids {
				if _, err := os.Stat(p); err != nil {
					t.Fatalf("check removed %s", p)
				}
			}
			real := newCycle(t)
			setup(real)
			ran, err := Run(context.Background(), real.o)
			if err != nil || ran.Failed {
				t.Fatalf("run = %+v, %v", ran, err)
			}
			rows := func(f *fixture, units []UnitResult) []string {
				var out []string
				for _, u := range units {
					out = append(out, strings.NewReplacer(f.alpha, "ALPHA", f.alphaID, "ID").Replace(u.Name)+" "+u.Action)
				}
				return out
			}
			if got, want := rows(check, previewed.Units), rows(real, ran.Units); !slices.Equal(got, want) {
				t.Fatalf("check units\n%q\nrun units\n%q", got, want)
			}
			for _, u := range previewed.Units {
				if u.Result != "would run" && !strings.HasPrefix(u.Result, "skipped: ") {
					t.Fatalf("check unit = %+v", u)
				}
			}
			if name == "both run" && len(previewed.Units) != 4 {
				t.Fatalf("check units = %+v", previewed.Units)
			}
		})
	}
}

func TestRepairStartsTheJournaledWatcherAndBoard(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.leaveJournal(t, false)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "repaired" || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	want := []string{"watcher @ " + f.alpha, "board @ " + f.alpha, "hand init @ " + f.alpha}
	if got := f.processCalls(t); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestRunRepairsBeforeFetching(t *testing.T) {
	f := newCycle(t)
	f.leaveJournal(t, false)
	f.srv.set(handAsset(), nil)
	if _, err := Run(context.Background(), f.o); err == nil {
		t.Fatal("a failed download succeeded")
	}
	want := []string{"watcher @ " + f.alpha, "board @ " + f.alpha, "hand init @ " + f.alpha}
	if got := f.processCalls(t); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	absent(t, filepath.Join(f.root, "update.json"))
}

func TestCheckReportsAnInterruptedUpdate(t *testing.T) {
	f := newCycle(t)
	f.current()
	f.leaveJournal(t, false)
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !slices.ContainsFunc(rep.Help, func(h string) bool {
		return strings.Contains(h, "stopped part way") && strings.Contains(h, "start the watcher in "+f.alpha) && strings.Contains(h, "start the board through "+f.alpha)
	}) {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, "update.json")); err != nil {
		t.Fatalf("check removed the journal: %v", err)
	}
}

func (f *fixture) stale() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = "0.0.1"
}

func (f *fixture) serverRuns(t *testing.T) {
	t.Helper()
	cmd := exec.Command(fakebin.Install(t, t.TempDir(), "sleep", "sleep", nil))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	end := func() { once.Do(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }) }
	t.Cleanup(end)
	marker, err := luvus.ProcStartMarker(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(f.sessionDir, "server.pid")
	if err := os.WriteFile(file, []byte(fmt.Sprintf("%d %s\n", cmd.Process.Pid, marker)), 0o600); err != nil {
		t.Fatal(err)
	}
	f.uhp.Handle("server.stop", func(json.RawMessage) (any, error) {
		f.note("server.stop")
		go func() { end(); _ = os.Remove(file) }()
		return map[string]any{}, nil
	})
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

func (f *fixture) leaveJournal(t *testing.T, supervisor bool) {
	t.Helper()
	j := map[string][]string{"watcher": {f.alpha}, "board": {f.alpha}, "init": {f.alpha}}
	if supervisor {
		j["supervisor"] = []string{f.alpha}
	}
	b, _ := json.Marshal(j)
	if err := os.WriteFile(filepath.Join(f.root, "update.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateStartsTheBoardThroughTheNextFleetWhenTheFirstFails(t *testing.T) {
	for name, fails := range map[string]int{"first unusable": 1, "none usable": 2} {
		t.Run(name, func(t *testing.T) {
			f := newCycle(t)
			newFleet(t, f.root, "beta")
			f.boardRuns(t)
			var tried []string
			f.o.Board = func(_ context.Context, home string) error {
				tried = append(tried, home)
				if len(tried) <= fails {
					return errors.New("unusable token")
				}
				return nil
			}
			rep, err := Run(context.Background(), f.o)
			if err != nil || rep.Failed != (fails == 2) {
				t.Fatalf("report = %+v, %v", rep, err)
			}
			if len(tried) != 2 || tried[0] == tried[1] {
				t.Fatalf("tried %q, want each fleet once", tried)
			}
			if got := rep.Units[len(rep.Units)-1]; fails == 1 && got != (UnitResult{"board", "start", "ok"}) {
				t.Fatalf("units = %+v", rep.Units)
			}
		})
	}
}
