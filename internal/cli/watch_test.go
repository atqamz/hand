package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err := os.WriteFile(filepath.Join(fx.h.vars["PATH"], "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func startWatch(t *testing.T, fx *attemptFixture) func() (string, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, _, code := fx.h.runCtx(ctx, "watch")
		done <- result{out, code}
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	return func() (string, int) {
		cancel()
		r := <-done
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
	log, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--app-name=hand hand a1 blocked: Do you want to proceed?", "--app-name=hand hand a1 quiet: turn ended"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("notifications = %q, missing %q", log, want)
		}
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
