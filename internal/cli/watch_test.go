package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func woken(fx *attemptFixture, kind string) bool {
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	return strings.Contains(out, ","+kind+",")
}

func fakeNotify(t *testing.T, fx *attemptFixture) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "notify.log")
	fakebin.Install(t, fx.h.vars["PATH"], notifierName(), "fake", map[string]string{"log": log})
	return log
}

func startWatch(t *testing.T, fx *attemptFixture, flags ...string) func() (string, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, _, code := fx.h.runCtx(ctx, append([]string{"watch"}, flags...)...)
		done <- result{out, code}
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	return func() (string, int) {
		cancel()
		r := <-done
		eventually(t, func() bool { return fx.rt.srv.Subscribers() == 0 })
		return r.out, r.code
	}
}

func TestWatchRecordsBlockedAndQuietTurnsOnce(t *testing.T) {
	fx := newAttemptFixture(t)
	notes := fakeNotify(t, fx)
	fx.start()
	stop := startWatch(t, fx)
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Do you want to proceed?" })
	blocked := map[string]any{"pane": "2", "status": "blocked", "agent": "claude"}
	fx.rt.srv.Publish("pane.agent_status_changed", blocked)
	fx.rt.srv.Publish("pane.agent_status_changed", blocked)
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "claude"})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "9", "status": "blocked", "agent": "claude"})
	out, code := stop()
	if code != 0 {
		t.Fatalf("watch exit = %d, out %q", code, out)
	}
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.blocked,"); n != 1 {
		t.Fatalf("attempt.blocked recorded %d times, want 1", n)
	}
	if !strings.Contains(out, `observed: "a1 blocked: Do you want to proceed?"`) {
		t.Fatalf("watch out = %q", out)
	}
	if runtime.GOOS == "windows" {
		t.Skip("desktop notifications are Unix-only")
	}
	name := filepath.Base(fx.h.home)
	for _, want := range []string{" " + name + " a1 blocked: Do you want to proceed?", " " + name + " a1 quiet: turn ended"} {
		eventually(t, func() bool {
			log, _ := os.ReadFile(notes)
			return strings.Contains(string(log), want)
		})
	}
}

func TestWatchReconnectsAfterARestartAndRecordsExits(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	fx.rt.srv.SetGeneration("gen-2")
	fx.rt.srv.DropSubscribers()
	eventually(t, func() bool { return woken(fx, "attempt.interrupted") })
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	fx.start()
	fx.rt.exitAll()
	fx.rt.srv.Publish("terminal.exited", map[string]any{"pane_id": "3", "terminal_id": "term-2", "server_generation": "gen-2"})
	eventually(t, func() bool { return woken(fx, "attempt.exited") })
	if _, code := stop(); code != 0 {
		t.Fatalf("watch exit = %d", code)
	}
}

func TestSecondWatcherIsRefused(t *testing.T) {
	fx := newAttemptFixture(t)
	stop := startWatch(t, fx)
	defer stop()
	if _, errOut, code := fx.h.run("watch"); code != 3 || !strings.Contains(errOut, "another hand watch is running") {
		t.Fatalf("second watch code=%d stderr=%q", code, errOut)
	}
}

func TestStopWhileWatchingRecordsStopped(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.closeDelay = 300 * time.Millisecond })
	out, errOut, code := fx.h.run("attempt", "stop", "a1")
	if code != 0 || !strings.Contains(out, "status: stopped") {
		t.Fatalf("stop with a watcher: code=%d out=%q stderr=%q", code, out, errOut)
	}
}

func TestWatchRecordsAQuietTurnAgainAfterAReconnect(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	done := map[string]any{"pane": "2", "status": "done", "agent": "claude"}
	fx.rt.srv.Publish("pane.agent_status_changed", done)
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	fx.rt.srv.DropSubscribers()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	fx.rt.srv.Publish("pane.agent_status_changed", done)
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 2 })
}

func TestWatchRecordsAQuietTurnWhenADeclinedPromptLeavesTheWorkerIdle(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Esc to cancel · Tab to amend" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
}

func TestWatchIgnoresAnIdlePaneBeforeTheWorkerWorks(t *testing.T) {
	fx := newAttemptFixture(t)
	stop := startWatch(t, fx)
	defer stop()
	fx.start()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Enter to confirm" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	if woken(fx, "attempt.quiet") {
		t.Fatal("an idle pane the watcher never saw working was recorded as a quiet turn")
	}
}

func TestWatchCatchesUpOnATurnThatEndedWhileItWasDown(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Esc to cancel · Tab to amend" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop = startWatch(t, fx)
	defer stop()
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
}

func TestWatchCatchesUpOnAScreenThatBlockedWhileItWasDown(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Esc to cancel · Tab to amend" })
	stop := startWatch(t, fx)
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	stop()
	stop = startWatch(t, fx, "--every", "100ms")
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "done" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.blocked,"); n != 1 {
		t.Fatalf("attempt.blocked recorded %d times across a restart, want 1", n)
	}
}

func TestWatchCatchesUpOnATurnASentMessageStarted(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	if err := openStore(t, fx.h).NoteAttempt(context.Background(), 1, "sent", "12 bytes"); err != nil {
		t.Fatal(err)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx)
	defer stop()
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
}

func TestWatchCatchesUpOnAScreenThatBlockedAfterAQuietTurn(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Esc to cancel · Tab to amend" })
	stop = startWatch(t, fx)
	defer stop()
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
}

func TestWatchLeavesAFreshIdleWorkerAloneAfterARestart(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Enter to confirm" })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	if woken(fx, "attempt.quiet") {
		t.Fatal("a worker idle since its launch was recorded as a quiet turn after a restart")
	}
}

func TestWatchReconcilesWithoutAnyEvent(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx, "--every", "100ms")
	defer stop()
	fx.rt.exitAll()
	eventually(t, func() bool { return woken(fx, "attempt.exited") })
}

func TestWatchIsNotBlockedByASlowNotifier(t *testing.T) {
	fx := newAttemptFixture(t)
	fakebin.Install(t, fx.h.vars["PATH"], notifierName(), "fake", map[string]string{"sleep": "5s"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	start := time.Now()
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	if time.Since(start) > 3*time.Second {
		t.Fatalf("quiet recorded after %s behind a slow notifier", time.Since(start))
	}
}

func TestOnlyWatchNeedsTheEventStream(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.rt.srv.Handle("uhp.capabilities", func(json.RawMessage) (any, error) {
		return map[string]any{"protocol": map[string]any{"name": "luvus-uhp", "major": 1}, "methods": luvus.Required, "server_generation": "gen-1"}, nil
	})
	fx.h.ok("attempt", "list")
	if _, errOut, code := fx.h.run("watch"); code == 0 || !strings.Contains(errOut, "lacks method events.subscribe") {
		t.Fatalf("watch without event stream: code=%d stderr=%q", code, errOut)
	}
}

func TestWatchDeliversPendingNotificationsBeforeExiting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("desktop notifications are Unix-only")
	}
	fx := newAttemptFixture(t)
	log := filepath.Join(t.TempDir(), "notify.log")
	fakebin.Install(t, fx.h.vars["PATH"], notifierName(), "fake", map[string]string{"sleep": "500ms", "log": log})
	fx.start()
	stop := startWatch(t, fx)
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	stop()
	got, _ := os.ReadFile(log)
	if !strings.Contains(string(got), "a1 quiet: turn ended") {
		t.Fatalf("notification lost at shutdown: %q", got)
	}
}

func TestQuietTurnSaysWhetherTheWorkerReported(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	done := map[string]any{"pane": "2", "status": "done", "agent": "claude"}
	working := map[string]any{"pane": "2", "status": "working", "agent": "claude"}
	fx.rt.srv.Publish("pane.agent_status_changed", done)
	eventually(t, func() bool {
		return strings.Contains(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), `"a1: turn ended without a new report"`)
	})
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	fx.rt.srv.Publish("pane.agent_status_changed", working)
	fx.rt.srv.Publish("pane.agent_status_changed", done)
	eventually(t, func() bool {
		return slices.ContainsFunc(turnEvents(t, fx), func(e state.Event) bool { return e.Detail == "a1: turn ended; reported r1 done" })
	})
}

func TestAutoresumeBringsTheSupervisorBack(t *testing.T) {
	for _, on := range []bool{true, false} {
		h, rt := newSupervisorFixture(t)
		policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"supervisor":{"autoresume":` + strconv.FormatBool(on) + `}}`
		if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
		startClaudeSupervisor(h)
		session := rt.lastCreate().Command[3]
		rt.set(func(rt *fakeRuntime) { rt.status = "blocked" })
		h.ok("supervisor", "send", "--text", "queued before the restart")
		rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
		rt.srv.SetGeneration("gen-2")
		stop := startWatch(t, &attemptFixture{h: h, rt: rt}, "--every", "1h")
		if !on {
			st := openStore(t, h)
			eventually(t, func() bool {
				sup, _, err := st.LatestSupervisor(context.Background())
				return err == nil && sup.Status == "interrupted"
			})
			time.Sleep(200 * time.Millisecond)
			stop()
			if n := len(rt.srv.Calls("terminal.backend.create")); n != 1 {
				t.Fatalf("autoresume off made %d create calls", n)
			}
			continue
		}
		st := openStore(t, h)
		eventually(t, func() bool {
			sup, _, err := st.LatestSupervisor(context.Background())
			return err == nil && sup.ID == 2 && sup.Status == "running"
		})
		eventually(t, func() bool { return slices.Contains(rt.prompts(), "queued before the restart") })
		out, _ := stop()
		if argv := rt.lastCreate().Command; !slices.Equal(argv[1:], []string{"--dangerously-skip-permissions", "--resume", session, "--model", "sonnet", "--effort", "low"}) {
			t.Fatalf("argv = %q", argv)
		}
		if !strings.Contains(out, "supervisor s2 resumed") {
			t.Fatalf("watch out = %q", out)
		}
	}
}

func wantPID(t *testing.T, path string) {
	t.Helper()
	var f []string
	eventually(t, func() bool {
		b, err := os.ReadFile(path)
		f = strings.Fields(string(b))
		return err == nil && len(f) == 2
	})
	m, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil || f[0] != strconv.Itoa(os.Getpid()) || f[1] != m {
		t.Fatalf("%s = %q, want %d %s (%v)", path, f, os.Getpid(), m, err)
	}
}

func TestPidFileWrittenAndRemoved(t *testing.T) {
	fx := newAttemptFixture(t)
	stop := startWatch(t, fx)
	wantPID(t, filepath.Join(fx.h.home, "watch.pid"))
	stop()
	if _, err := os.Stat(filepath.Join(fx.h.home, "watch.pid")); !os.IsNotExist(err) {
		t.Fatalf("watch.pid after exit: %v", err)
	}
	_, stopBoard := startBoard(t, fx.h, "127.0.0.1")
	wantPID(t, filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.pid"))
	stopBoard()
	if _, err := os.Stat(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.pid")); !os.IsNotExist(err) {
		t.Fatalf("board.pid after exit: %v", err)
	}
}

func TestWatchRunsWhenItsPidFileIsUnwritable(t *testing.T) {
	fx := newAttemptFixture(t)
	pid := filepath.Join(fx.h.home, "watch.pid")
	if err := os.Mkdir(pid, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		errOut string
		code   int
	}
	done := make(chan result, 1)
	go func() {
		_, errOut, code := fx.h.runCtx(ctx, "watch")
		done <- result{errOut, code}
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	if _, errOut, code := fx.h.runCtx(context.Background(), "watch"); code != 3 || !strings.Contains(errOut, "another hand watch is running") {
		t.Fatalf("second watch = %d %q; want the lock held", code, errOut)
	}
	cancel()
	r := <-done
	if r.code != 0 || !strings.Contains(r.errOut, "warning: cannot write "+pid+": ") || !strings.Contains(r.errOut, "; hand update on Windows will not stop this process\n") {
		t.Fatalf("watch = %d %q", r.code, r.errOut)
	}
}

func TestBoardRunsWhenItsPidFileIsUnwritable(t *testing.T) {
	h := newHarness(t)
	pid := filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid")
	if err := os.Mkdir(pid, 0o700); err != nil {
		t.Fatal(err)
	}
	_, stop := startBoard(t, h, "127.0.0.1")
	if out := stop(); !strings.Contains(out, "warning: cannot write "+pid+": ") {
		t.Fatalf("board output = %q", out)
	}
}

func turnEvents(t *testing.T, fx *attemptFixture) []state.Event {
	t.Helper()
	events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.quiet", "attempt.idle"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRepeatedQuietWakesOnce(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	for range 3 {
		fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "claude"})
		fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	}
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 3 })
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.quiet,"); n != 1 {
		t.Fatalf("three quiet turns woke the supervisor %d times, want 1: %+v", n, turnEvents(t, fx))
	}
}

func TestQuietRightAfterReportDoesNotWake(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	if woken(fx, "attempt.quiet") {
		t.Fatalf("a turn that ended right after its report woke the supervisor twice: %+v", turnEvents(t, fx))
	}
}

func TestQuietAfterDoneDoesNotWake(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	fx.h.now = fx.h.now.Add(time.Hour)
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if strings.Contains(out, ",attempt.quiet,") || !strings.Contains(out, `,attempt.idle,t1,"a1: turn ended; reported r1 done"`) {
		t.Fatalf("a turn that ended an hour after a done report woke, or did not ride with the report: %q", out)
	}
}

func TestQuietAfterProgressWakes(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "progress", "--text", "Halfway")
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "idle", "agent": "claude"})
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	if !woken(fx, "attempt.quiet") {
		t.Fatalf("a turn that ended right after a progress report did not wake: %+v", turnEvents(t, fx))
	}
}

func TestWatchStartSurvivesAProbe(t *testing.T) {
	fx := newAttemptFixture(t)
	probe, err := os.OpenFile(filepath.Join(fx.h.home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := flock.Lock(probe, false); !ok || err != nil {
		t.Fatalf("probe lock = %v, %v", ok, err)
	}
	time.AfterFunc(20*time.Millisecond, func() { _ = probe.Close() })
	stop := startWatch(t, fx)
	if out, code := stop(); code != 0 {
		t.Fatalf("watch exit = %d, out %q", code, out)
	}
}

func TestWatchReconcilesOnATerminalExitOrPaneClose(t *testing.T) {
	for event, data := range map[string]map[string]any{
		"terminal.exited": {"pane_id": "2", "terminal_id": "term-1", "server_generation": "gen-1"},
		"pane.closed":     {"pane_id": "2"},
	} {
		t.Run(event, func(t *testing.T) {
			fx := newAttemptFixture(t)
			fx.start()
			stop := startWatch(t, fx)
			t.Cleanup(func() { stop() })
			eventually(t, func() bool { return explains(fx.rt) > 0 })
			fx.rt.exitAll()
			fx.rt.srv.Publish(event, data)
			eventually(t, func() bool { return woken(fx, "attempt.exited") })
			if n := len(fx.rt.srv.Calls("events.subscribe")); n != 1 {
				t.Fatalf("subscribed %d times, want 1", n)
			}
		})
	}
}

func TestWatchReconnectsOnResyncRequired(t *testing.T) {
	fx := newAttemptFixture(t)
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("events.resync_required", map[string]any{})
	eventually(t, func() bool { return len(fx.rt.srv.Calls("events.subscribe")) == 2 })
}

const claudeLimit = "● Fixing login\n  ⎿  You've hit your session limit · resets 6:10am (Asia/Jakarta)\n\n> "

func TestWatchRecordsALimitedTurnInsteadOfQuiet(t *testing.T) {
	fx := newAttemptFixture(t)
	notes := fakeNotify(t, fx)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if !strings.Contains(got, `,attempt.limited,t1,"a1: You've hit your session limit · resets 6:10am (Asia/Jakarta)"`) || strings.Contains(got, ",attempt.quiet,") {
		t.Fatalf("wait = %q", got)
	}
	if runtime.GOOS == "windows" {
		t.Skip("desktop notifications are Unix-only")
	}
	eventually(t, func() bool {
		log, _ := os.ReadFile(notes)
		return strings.Contains(string(log), "a1 limited: You've hit your session limit")
	})
}

func TestWatchCatchesUpOnALimitedTurnOnce(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	stop := startWatch(t, fx)
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "done" })
	stop = startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if n := strings.Count(got, ",attempt.limited,"); n != 1 || strings.Contains(got, ",attempt.quiet,") {
		t.Fatalf("attempt.limited recorded %d times across a restart: %q", n, got)
	}
}

func TestWatchRecordsAQuietTurnWhileAnEarlierLimitLineStaysOnScreen(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	screen := strings.TrimSuffix(claudeLimit, "> ")
	for i, line := range []string{"● Fixed login", "● Checked the tests", "  ⎿  You've hit your weekly limit · resets Sep 27, 5pm (Asia/Jakarta)"} {
		screen += line + "\n\n"
		fx.rt.set(func(rt *fakeRuntime) { rt.screen = screen + "> " })
		fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "claude"})
		fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
		eventually(t, func() bool {
			return len(turnEvents(t, fx))+strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.limited,") == i+2
		})
	}
	got := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if n := strings.Count(got, ",attempt.limited,"); n != 2 || !strings.Contains(got, "a1: You've hit your weekly limit") || len(turnEvents(t, fx)) != 2 {
		t.Fatalf("an earlier limit line was recorded again, or a new one was missed: %q", got)
	}
}

func TestWatchRecordsAnAgyLimitOnceWhileItStaysOnScreen(t *testing.T) {
	fx := agyFixture(t)
	fx.startAgy()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) {
		rt.screen = "⚠ Individual quota reached. Please upgrade your subscription to increase your\n  limits. Resets in 118h19m26s.\n\n> "
	})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "agy"})
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	time.Sleep(1100 * time.Millisecond)
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "agy"})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "agy"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.limited,"); n != 1 {
		t.Fatalf("one agy quota line recorded %d times", n)
	}
}

func TestWatchRecordsNoTurnWhileItCannotReadTheScreen(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.screen, rt.readFail = "done", claudeLimit, "internal" })
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return len(fx.rt.srv.Calls("agent.read")) >= 2 })
	if events := turnEvents(t, fx); len(events) != 0 {
		t.Fatalf("a turn was recorded without its screen: %+v", events)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.readFail = "" })
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	if events := turnEvents(t, fx); len(events) != 0 {
		t.Fatalf("turn events = %+v", events)
	}
}

func TestWatchRecordsAQuietTurnAfterTheLimitClears(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.limited") })
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = "● Fixed login\n\n> " })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "working", "agent": "claude"})
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.quiet") })
	if n := strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.limited,"); n != 1 {
		t.Fatalf("attempt.limited recorded %d times, want 1", n)
	}
}

func TestALimitedTurnAfterADoneReportStillWakes(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	cursor := field(fx.h.ok("orient"), "cursor")
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	got := fx.h.ok("wait", "--after", cursor, "--timeout", "5s")
	if !strings.Contains(got, ",attempt.limited,") || strings.Contains(got, ",attempt.idle,") {
		t.Fatalf("wait after a done report = %q", got)
	}
}

func TestWatchCatchesUpOnAnIdleTurnOnce(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx)
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "done" })
	stop = startWatch(t, fx)
	defer stop()
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "blocked", "agent": "claude"})
	eventually(t, func() bool { return woken(fx, "attempt.blocked") })
	if events := turnEvents(t, fx); len(events) != 1 || events[0].Kind != "attempt.idle" {
		t.Fatalf("turn events across a restart = %+v", events)
	}
}

func shortLongTurn(t *testing.T, d time.Duration) {
	t.Helper()
	old := *cli.LongTurn
	*cli.LongTurn = d
	t.Cleanup(func() { *cli.LongTurn = old })
}

func longEvents(fx *attemptFixture) int {
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	return strings.Count(out, ",attempt.long,")
}

func TestLongTurnWakesOnce(t *testing.T) {
	shortLongTurn(t, 150*time.Millisecond)
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	eventually(t, func() bool { return woken(fx, "attempt.long") })
	time.Sleep(500 * time.Millisecond)
	out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms")
	if n := strings.Count(out, ",attempt.long,"); n != 1 || !strings.Contains(out, `,attempt.long,t1,"a1: working for 60m"`) {
		t.Fatalf("a turn that kept working recorded attempt.long %d times: %q", n, out)
	}
}

func TestLongTurnResetsPerTurn(t *testing.T) {
	shortLongTurn(t, 150*time.Millisecond)
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	eventually(t, func() bool { return longEvents(fx) == 1 })
	publishStatus(fx, "2", "idle")
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
	publishStatus(fx, "2", "working")
	eventually(t, func() bool { return longEvents(fx) == 2 })
}

func TestShortTurnDoesNotWake(t *testing.T) {
	shortLongTurn(t, 10*time.Second)
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	time.Sleep(500 * time.Millisecond)
	if woken(fx, "attempt.long") {
		t.Fatal("a turn shorter than the threshold woke the supervisor")
	}
}

func TestWatchCatchesUpOnATurnThatEndedAfterALongTurn(t *testing.T) {
	shortLongTurn(t, 150*time.Millisecond)
	fx := newAttemptFixture(t)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	eventually(t, func() bool { return longEvents(fx) == 1 })
	stop()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop = startWatch(t, fx, "--every", "30ms")
	defer stop()
	eventually(t, func() bool { return len(turnEvents(t, fx)) == 1 })
}

func waitTicks(t *testing.T, fx *attemptFixture) {
	t.Helper()
	count := func() (n int) {
		fx.rt.set(func(rt *fakeRuntime) { n = rt.validations })
		return n
	}
	want := count() + 3
	eventually(t, func() bool { return count() >= want })
}

func TestRestartedWatchKeepsTheTurnClock(t *testing.T) {
	shortLongTurn(t, time.Hour)
	fx := newAttemptFixture(t)
	fx.h.now = time.Now().Add(-2 * time.Hour)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	eventually(t, func() bool { return longEvents(fx) == 1 })
	waitTicks(t, fx)
	if n := longEvents(fx); n != 1 {
		t.Fatalf("a turn begun before the watcher started recorded attempt.long %d times", n)
	}
}

func TestRestartedWatchDoesNotRepeatALongTurn(t *testing.T) {
	shortLongTurn(t, time.Hour)
	fx := newAttemptFixture(t)
	fx.h.now = time.Now().Add(-2 * time.Hour)
	fx.start()
	stop := startWatch(t, fx, "--every", "30ms")
	eventually(t, func() bool { return longEvents(fx) == 1 })
	stop()
	stop = startWatch(t, fx, "--every", "30ms")
	defer stop()
	waitTicks(t, fx)
	if n := longEvents(fx); n != 1 {
		t.Fatalf("a restart after attempt.long recorded %d of them", n)
	}
}
