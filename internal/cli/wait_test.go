package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestWaitReturnsOnlyWakeEventsAfterTheCursor(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "start", "t1")
	cursor := field(h.ok("orient"), "cursor")
	h.ok("decision", "ask", "t1", "Keep it?")
	h.ok("decision", "answer", "d1", "yes")
	got := h.ok("wait", "--after", cursor, "--timeout", "2s")
	if !strings.Contains(got, "events[1]{seq,kind,task,detail}:") || !strings.Contains(got, ",decision.answered,t1,d1") {
		t.Fatalf("wait = %q", got)
	}
	next := field(got, "cursor")
	empty := h.ok("wait", "--after", next, "--timeout", "300ms")
	if !strings.Contains(empty, "events[0]{seq,kind,task,detail}:") || field(empty, "cursor") != next {
		t.Fatalf("timed-out wait = %q", empty)
	}
}

func TestWaitBlocksUntilAWakeEventArrives(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "start", "t1")
	h.ok("decision", "ask", "t1", "Keep it?")
	cursor := field(h.ok("orient"), "cursor")
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		out, _, code := h.run("wait", "--after", cursor, "--timeout", "10s")
		done <- result{out, code}
	}()
	time.Sleep(400 * time.Millisecond)
	h.ok("decision", "answer", "d1", "yes")
	r := <-done
	if r.code != 0 || !strings.Contains(r.out, ",decision.answered,t1,d1") || time.Since(start) > 5*time.Second {
		t.Fatalf("wait = %q, code %d, after %s", r.out, r.code, time.Since(start))
	}
}

func TestWaitRejectsABadTimeout(t *testing.T) {
	h := initWithProject(t)
	for _, bad := range []string{"soon", "-1s"} {
		if _, _, code := h.run("wait", "--timeout", bad); code != 2 {
			t.Fatalf("--timeout %s code = %d, want 2", bad, code)
		}
	}
}

func TestProgressReportDoesNotWake(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "progress", "--text", "Halfway")
	if out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(out, ",attempt.reported,") {
		t.Fatalf("a progress report woke the supervisor: %q", out)
	}
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	if out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(out, `,attempt.reported,t1,"a1: r1 progress"`) || !strings.Contains(out, `,attempt.reported,t1,"a1: r2 done"`) {
		t.Fatalf("the progress report did not ride in the next wake: %q", out)
	}
}

func TestAFullBatchOfRidersStillWakes(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	st := openStore(t, fx.h)
	for range 51 {
		if _, err := st.AddReport(context.Background(), 1, state.ReportProgress, "Halfway"); err != nil {
			t.Fatal(err)
		}
	}
	if out := fx.h.ok("wait", "--after", "0", "--timeout", "1ms"); !strings.Contains(out, "events[50]") {
		t.Fatalf("fifty progress reports kept every later wake behind them: %q", out)
	}
}
