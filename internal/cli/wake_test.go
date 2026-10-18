package cli_test

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestWakeArmsListsAndCancelsTimers(t *testing.T) {
	fx, _ := stallFleet(t, "")
	h := fx.h
	if out := h.ok("wake", "--after", "30m", "--note", "check PR 909"); out != "timer: w1\nstatus: armed\ndue: \"2026-09-26T00:30:00Z\"\nnote: check PR 909\n" {
		t.Fatalf("after = %q", out)
	}
	if out := h.ok("wake", "--at", "2026-09-26T09:00:00+07:00"); out != "timer: w2\nstatus: armed\ndue: \"2026-09-26T02:00:00Z\"\n" {
		t.Fatalf("at = %q", out)
	}
	if out := h.ok("wake", "--every", "30m", "--while", "t1", "--note", "poll CI"); out != "timer: w3\nstatus: armed\ndue: \"2026-09-26T00:30:00Z\"\nevery: 30m\nwhile: t1\nnote: poll CI\n" {
		t.Fatalf("every = %q", out)
	}
	want := "timers[3]{id,due,every,while,note}:\n" +
		"  w1,\"2026-09-26T00:30:00Z\",\"\",\"\",check PR 909\n" +
		"  w3,\"2026-09-26T00:30:00Z\",30m,t1,poll CI\n" +
		"  w2,\"2026-09-26T02:00:00Z\",\"\",\"\",\"\"\n"
	if out := h.ok("wake", "list"); out != want {
		t.Fatalf("list =\n%s\nwant\n%s", out, want)
	}
	if out := h.ok("orient"); !strings.Contains(out, "timers[3]{id,due,note}:\n  w1,") || strings.Contains(out, "unwatched") {
		t.Fatalf("orient = %q", out)
	}
	has(t, "cancel", h.ok("wake", "cancel", "w2"), "timer: w2\nstatus: cancelled\n")
	if _, errOut, code := h.run("wake", "cancel", "w2"); code != 3 || !strings.Contains(errOut, "timer w2 is already cancelled") {
		t.Fatalf("cancel twice: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("wake", "cancel", "w9"); code != 3 || !strings.Contains(errOut, "not found: timer w9") {
		t.Fatalf("cancel missing: code=%d stderr=%q", code, errOut)
	}
	h.ok("task", "done", "t1")
	if out := h.ok("wake", "list"); out != "timers[1]{id,due,every,while,note}:\n  w1,\"2026-09-26T00:30:00Z\",\"\",\"\",check PR 909\n" {
		t.Fatalf("list after done = %q", out)
	}
}

func TestWakeRefusesBadTimers(t *testing.T) {
	fx, _ := stallFleet(t, "")
	h := fx.h
	for _, c := range []struct {
		args []string
		code int
		err  string
	}{
		{nil, 2, "usage: hand wake --after DURATION | --at TIME | --every DURATION [--while tN] [--note TEXT]; hand wake list; hand wake cancel wN. A timer counts as watching only when it is due within 6 hours"},
		{[]string{"soon"}, 2, "usage: hand wake --after"},
		{[]string{"--note", "x"}, 2, "wake: give exactly one of --after, --at or --every"},
		{[]string{"--after", "30m", "--every", "1h"}, 2, "wake: give exactly one of --after, --at or --every"},
		{[]string{"--after", "59s"}, 2, "a timer must be due at least 1m from now"},
		{[]string{"--every", "30s"}, 2, "a timer repeats at most once a minute"},
		{[]string{"--at", "tomorrow"}, 2, `wake: --at "tomorrow" is not an RFC 3339 time like YYYY-MM-DDTHH:MM:SSZ`},
		{[]string{"--at", "2026-09-25T23:00:00Z"}, 2, "a timer must be due at least 1m from now"},
		{[]string{"--after", "1h", "--note", strings.Repeat("x", 201)}, 2, "a timer note is at most 200 characters"},
		{[]string{"--after", "1h", "--note", "two\nlines"}, 2, "a timer note is one line"},
		{[]string{"--after", "1h", "--while", "w1"}, 2, `"w1" is not a t id`},
		{[]string{"--after", "1h", "--while", "t9"}, 3, "not found: task t9"},
		{[]string{"--after", "1h", "extra"}, 2, "wake: want 0 argument(s) after flags, got 1"},
		{[]string{"cancel"}, 2, "wake cancel: want 1 argument(s)"},
		{[]string{"cancel", "t1"}, 2, `"t1" is not a w id`},
		{[]string{"list", "x"}, 2, "wake list: want 0 argument(s)"},
	} {
		if _, errOut, code := h.run(append([]string{"wake"}, c.args...)...); code != c.code || !strings.Contains(errOut, c.err) {
			t.Fatalf("hand wake %q: code=%d stderr=%q, want %d and %q", c.args, code, errOut, c.code, c.err)
		}
	}
	h.ok("wake", "--after", "1m", "--while", "t1")
	for range 19 {
		h.ok("wake", "--every", "1h")
	}
	if _, errOut, code := h.run("wake", "--after", "1h"); code != 3 || !strings.Contains(errOut, "20 timers are armed, the most a fleet keeps; cancel one first: `hand wake cancel wN`") {
		t.Fatalf("21st timer: code=%d stderr=%q", code, errOut)
	}
	h.ok("task", "abandon", "t1")
	h.ok("wake", "--after", "1h")
}

func timerWakes(fx *attemptFixture) []string {
	var wakes []string
	for _, p := range fx.rt.prompts() {
		if strings.Contains(p, "\ntimer.fired ") {
			wakes = append(wakes, p)
		}
	}
	return wakes
}

func wantTimerWake(t *testing.T, fx *attemptFixture, n int, want string) {
	t.Helper()
	eventually(t, func() bool { return len(timerWakes(fx)) >= n })
	if got := timerWakes(fx); len(got) != n || got[n-1] != "[hand v1 wake]\n"+want {
		t.Fatalf("timer wakes = %q, want %d ending with %q", got, n, want)
	}
}

func firedEvents(fx *attemptFixture) int {
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	return strings.Count(out, ",timer.fired,")
}

func TestATimerWakesTheSupervisorAndHoldsTheStallWake(t *testing.T) {
	fx, log := stallFleet(t, "")
	fx.h.ok("wake", "--after", "30m", "--note", "check PR 909")
	stop := startWatch(t, fx, "--every", "30ms")
	stallTick(t, fx)
	fx.h.advance(29 * time.Minute)
	stallTick(t, fx)
	if n, fired := stalls(fx), firedEvents(fx); n != 0 || fired != 0 {
		t.Fatalf("before the timer: %d stalls, %d fired", n, fired)
	}
	if out := fx.h.ok("orient"); strings.Contains(out, "unwatched") || !strings.Contains(out, "timers[1]{id,due,note}:\n  w1,\"2026-09-26T00:30:00Z\",check PR 909\n") {
		t.Fatalf("orient = %q", out)
	}
	fx.h.advance(time.Minute)
	wantTimerWake(t, fx, 1, "timer.fired w1: check PR 909")
	if out := fx.h.ok("orient"); strings.Contains(out, "timers") || !strings.Contains(out, "unwatched[1]:\n  - t1\n") {
		t.Fatalf("orient after the timer = %q", out)
	}
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
	out, _ := stop()
	if !strings.Contains(out, `observed: "timer.fired w1: check PR 909"`) {
		t.Fatalf("watch out = %q", out)
	}
	if alerts := stallAlerts(log); len(alerts) != 0 {
		t.Fatalf("alerts = %q", alerts)
	}
}

func TestARepeatingTimerWakesUntilItsTaskFinishes(t *testing.T) {
	fx, _ := stallFleet(t, `,"stall":"off"`)
	fx.h.ok("wake", "--every", "10m", "--while", "t1", "--note", "poll CI")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	stallTick(t, fx)
	fx.h.advance(10 * time.Minute)
	wantTimerWake(t, fx, 1, `timer.fired w1: poll CI (t1 "Fix login")`)
	stallTick(t, fx)
	fx.h.advance(10 * time.Minute)
	wantTimerWake(t, fx, 2, `timer.fired w1: poll CI (t1 "Fix login")`)
	fx.h.ok("task", "done", "t1")
	fx.h.advance(10 * time.Minute)
	stallTick(t, fx)
	if n := firedEvents(fx); n != 2 {
		t.Fatalf("fired %d times, want 2", n)
	}
	if out := fx.h.ok("wake", "list"); out != "timers[0]{id,due,every,while,note}:\n" {
		t.Fatalf("list = %q", out)
	}
}

func TestATimerDueWhileTheWatcherIsDownIsDeliveredLateOnce(t *testing.T) {
	fx, _ := stallFleet(t, `,"stall":"off"`)
	fx.h.ok("wake", "--after", "30m", "--note", "check PR 909")
	fx.h.advance(2 * time.Hour)
	stop := startWatch(t, fx, "--every", "30ms")
	wantTimerWake(t, fx, 1, "timer.fired w1, 1h30m late: check PR 909")
	stop()
	stop = startWatch(t, fx, "--every", "30ms")
	defer stop()
	stallTick(t, fx)
	if n, wakes := firedEvents(fx), len(timerWakes(fx)); n != 1 || wakes != 1 {
		t.Fatalf("after a restart: %d fired, %d wakes", n, wakes)
	}
}

func TestATimerWaitsForASupervisorAndReachesTheNextOne(t *testing.T) {
	fx, _ := stallFleet(t, `,"stall":"off"`)
	fx.h.ok("wake", "--after", "30m", "--note", "after the switch")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	fx.h.ok("supervisor", "stop")
	noStall(t, fx)
	if n := firedEvents(fx); n != 0 {
		t.Fatalf("fired %d times with no supervisor", n)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	has(t, "start", fx.h.ok("supervisor", "start"), "supervisor: s2")
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	wantTimerWake(t, fx, 1, "timer.fired w1, 23h30m late: after the switch")
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	fired := regexp.MustCompile(`(?m)^  ([0-9]+),timer\.fired,`).FindStringSubmatch(out)
	if fired == nil {
		t.Fatalf("wait = %q", out)
	}
	eventually(t, func() bool { return field(fx.h.ok("supervisor", "show"), "wake_cursor") == fired[1] })
	fx.h.ok("wake", "--after", "30m", "--note", "after the resume")
	fx.h.ok("supervisor", "stop")
	has(t, "resume", fx.h.ok("supervisor", "resume"), "supervisor: s3")
	stallTick(t, fx)
	fx.h.advance(30 * time.Minute)
	wantTimerWake(t, fx, 2, "timer.fired w2: after the resume")
	has(t, "show", fx.h.ok("supervisor", "show"), "supervisor: s3")
}

func TestAFarTimerDoesNotHoldTheStallWake(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.h.ok("wake", "--after", "7h", "--note", "check PR 909")
	if out := fx.h.ok("orient"); !strings.Contains(out, "unwatched[1]:\n  - t1\n") || !strings.Contains(out, "(beyond the horizon) check PR 909") {
		t.Fatalf("orient = %q", out)
	}
	stop := startWatch(t, fx, "--every", "30ms")
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m; w1 is armed but due in 6h40m")
	fx.h.advance(time.Hour)
	fx.h.advance(5 * time.Hour)
	stallTick(t, fx)
	if out := fx.h.ok("orient"); strings.Contains(out, "unwatched") || strings.Contains(out, "beyond") {
		t.Fatalf("orient once the timer is within the horizon = %q", out)
	}
	stop()
}

func TestWakeIsRefusedWithoutAManagedSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	gen := fx.rt.srv.Generation()
	refused := "timers fire only while a managed supervisor runs, and none does: start one with `hand supervisor start`; a supervisor opened by hand waits with `hand wait --timeout` instead"
	check := func(when string) {
		t.Helper()
		if out, errOut, code := fx.h.run("wake", "--after", "30m"); code != 3 || out != "" || !strings.Contains(errOut, refused) {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", when, code, out, errOut)
		}
		if out := fx.h.ok("wake", "list"); out != "timers[0]{id,due,every,while,note}:\n" {
			t.Fatalf("%s: list = %q", when, out)
		}
	}
	check("no supervisor")
	startClaudeSupervisor(fx.h)
	has(t, "wake", fx.h.ok("wake", "--after", "30m"), "timer: w1")
	fx.rt.srv.SetGeneration("gen-2")
	if _, errOut, code := fx.h.run("wake", "--after", "30m"); code != 3 || !strings.Contains(errOut, refused) {
		t.Fatalf("stale generation: code=%d stderr=%q", code, errOut)
	}
	has(t, "cancel", fx.h.ok("wake", "cancel", "w1"), "status: cancelled")
	fx.rt.srv.SetGeneration(gen)
	fx.h.ok("supervisor", "stop")
	check("stopped supervisor")
}

func TestOrientShowsUnwatchedWorkWhenNoManagedSupervisorFiresTheTimer(t *testing.T) {
	fx, _ := stallFleet(t, "")
	gen := fx.rt.srv.Generation()
	fx.h.ok("wake", "--after", "30m", "--note", "check PR 909")
	if out := fx.h.ok("orient"); strings.Contains(out, "unwatched") || !strings.Contains(out, "w1,\"2026-09-26T00:30:00Z\",check PR 909\n") {
		t.Fatalf("orient with a managed supervisor = %q", out)
	}
	fx.rt.srv.SetGeneration("gen-2")
	if out := fx.h.ok("orient"); !strings.Contains(out, "unwatched[1]:\n  - t1\n") || !strings.Contains(out, "w1,\"2026-09-26T00:30:00Z\",\"(no managed supervisor: not fired) check PR 909\"\n") {
		t.Fatalf("orient with a stale generation = %q", out)
	}
	fx.rt.srv.SetGeneration(gen)
	fx.h.ok("supervisor", "stop")
	if out := fx.h.ok("orient"); !strings.Contains(out, "unwatched[1]:\n  - t1\n") || !strings.Contains(out, "\"(no managed supervisor: not fired) check PR 909\"\n") {
		t.Fatalf("orient without a supervisor = %q", out)
	}
}
