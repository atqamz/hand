package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
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
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(fx.h.vars["PATH"], notifierName()), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
	eventually(t, func() bool {
		return strings.Count(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), ",attempt.quiet,") == 2
	})
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
	if err := os.WriteFile(filepath.Join(fx.h.vars["PATH"], notifierName()), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	fx := newAttemptFixture(t)
	log := filepath.Join(t.TempDir(), "notify.log")
	script := "#!/bin/sh\nsleep 0.5\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(fx.h.vars["PATH"], notifierName()), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
		return strings.Contains(fx.h.ok("wait", "--after", "0", "--timeout", "1ms"), `"a1: turn ended; reported r1 done"`)
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
