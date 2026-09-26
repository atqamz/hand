package cli_test

import (
	"strings"
	"testing"
	"time"
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
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		out, _, code := h.run("wait", "--timeout", "10s")
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
	if _, _, code := h.run("wait", "--timeout", "soon"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
