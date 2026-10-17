package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

func stallFleet(t *testing.T, stall string) (*attemptFixture, string) {
	t.Helper()
	fx := newAttemptFixture(t)
	log := filepath.Join(t.TempDir(), "notify.log")
	fakebin.Install(t, fx.h.vars["PATH"], "hand-notify", "notifyenv", map[string]string{"log": log})
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"notify":["hand-notify"]` + stall + `}`
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	startClaudeSupervisor(fx.h)
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	return fx, log
}

func stalls(fx *attemptFixture) int {
	out, _, _ := fx.h.run("wait", "--after", "0", "--timeout", "1ms")
	return strings.Count(out, ",supervisor.stalled,")
}

func stallWake(fx *attemptFixture, n int) string {
	var wakes []string
	for _, p := range fx.rt.prompts() {
		if strings.Contains(p, "\nsupervisor.stalled ") {
			wakes = append(wakes, p)
		}
	}
	if len(wakes) < n {
		return ""
	}
	return wakes[n-1]
}

func stallAlerts(log string) []string {
	b, _ := os.ReadFile(log)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "supervisor.stalled|") {
			out = append(out, line)
		}
	}
	return out
}

func stallTick(t *testing.T, fx *attemptFixture) {
	t.Helper()
	want := explains(fx.rt) + 5
	eventually(t, func() bool { return explains(fx.rt) >= want })
}

func noStall(t *testing.T, fx *attemptFixture) {
	t.Helper()
	root := fx.h.vars["SECONDHAND_HOME"]
	ids, err := os.ReadDir(filepath.Join(root, "fleets"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("fleets = %v, %v", ids, err)
	}
	fx.h.advance(24 * time.Hour)
	snapshot := filepath.Join(root, "backups", ids[0].Name(), "hand-"+fx.h.clock().UTC().Format("20060102")+".db")
	eventually(t, func() bool { _, err := os.Stat(snapshot); return err == nil })
	if n := stalls(fx); n != 0 {
		t.Fatalf("supervisor.stalled recorded %d times", n)
	}
}

func wantStall(t *testing.T, fx *attemptFixture, n int, text string) {
	t.Helper()
	eventually(t, func() bool { return stallWake(fx, n) != "" })
	want := "[hand v1 wake]\nsupervisor.stalled s1: " + text + ". Start a worker, arm a timer with `hand wake`, or finish the task."
	if got := stallWake(fx, n); got != want {
		t.Fatalf("stall wake %d =\n%q\nwant\n%q", n, got, want)
	}
}

func TestStallWakesTheIdleSupervisorThenBacksOff(t *testing.T) {
	fx, log := stallFleet(t, "")
	stop := startWatch(t, fx, "--every", "30ms")
	stallTick(t, fx)
	fx.h.advance(19 * time.Minute)
	stallTick(t, fx)
	if n := stalls(fx); n != 0 {
		t.Fatalf("stalled before the delay: %d", n)
	}
	fx.h.advance(time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
	for _, step := range []struct {
		wait time.Duration
		idle string
	}{{40 * time.Minute, "1h"}, {time.Hour, "2h"}, {time.Hour, "3h"}} {
		n := stalls(fx)
		fx.h.advance(step.wait - time.Minute)
		stallTick(t, fx)
		if got := stalls(fx); got != n {
			t.Fatalf("woke %v early: %d stalls, want %d", time.Minute, got, n)
		}
		fx.h.advance(time.Minute)
		wantStall(t, fx, n+1, "t1 is active, no attempt is running, idle "+step.idle)
	}
	out, _ := stop()
	if n := stalls(fx); n != 4 {
		t.Fatalf("stalls = %d, want 4", n)
	}
	alerts := stallAlerts(log)
	if len(alerts) != 1 || alerts[0] != "supervisor.stalled|supervisor|s1 stalled: t1 is active, no attempt is running, idle 1h|" {
		t.Fatalf("alerts = %q", alerts)
	}
	if !strings.Contains(out, `observed: "s1 stalled: t1 is active, no attempt is running, idle 20m"`) {
		t.Fatalf("watch out = %q", out)
	}
}

func TestAnyOtherEventStartsANewStallEpisode(t *testing.T) {
	fx, log := stallFleet(t, "")
	fx.h.ok("task", "add", "app", "Write docs")
	fx.h.ok("task", "start", "t2")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 and t2 are active, no attempt is running, idle 20m")
	fx.h.ok("task", "add", "app", "Later")
	fx.h.advance(19 * time.Minute)
	stallTick(t, fx)
	if n := stalls(fx); n != 1 {
		t.Fatalf("stalls = %d before the new episode's delay", n)
	}
	fx.h.advance(time.Minute)
	wantStall(t, fx, 2, "t1 and t2 are active, no attempt is running, idle 20m")
	if alerts := stallAlerts(log); len(alerts) != 0 {
		t.Fatalf("alerts before 60m idle = %q", alerts)
	}
}

func TestStallWaitsAfterTheSupervisorWorks(t *testing.T) {
	fx, _ := stallFleet(t, "")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	stallTick(t, fx)
	fx.h.advance(15 * time.Minute)
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	stallTick(t, fx)
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.revision = "idle", rt.revision+1 })
	fx.h.advance(19 * time.Minute)
	stallTick(t, fx)
	if n := stalls(fx); n != 0 {
		t.Fatalf("stalled %d times within 20m of the supervisor's last turn", n)
	}
	fx.h.advance(time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 35m")
}

func TestNoStallWhileAnAttemptRuns(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	fx.h.ok("attempt", "stop", "a1")
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
}

func TestNoStallWhileADecisionIsOpen(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.h.ok("decision", "ask", "t1", "Keep the old cookie name?")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	fx.h.ok("decision", "answer", "d1", "yes")
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
}

func TestNoStallWithoutAnActiveTask(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.h.ok("task", "abandon", "t1")
	fx.h.ok("task", "add", "app", "Later")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
}

func TestNoStallForAStoppedSupervisor(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.h.ok("supervisor", "stop")
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
}

func TestNoStallForABlockedSupervisor(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Do you want to proceed?" })
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stallTick(t, fx)
	fx.h.advance(time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 24h1m")
}

func TestNoStallForALimitedSupervisor(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit })
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	fx.rt.set(func(rt *fakeRuntime) { rt.screen = "> " })
	fx.h.advance(time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 24h1m")
}

func TestNoStallWhileASwitchIsPending(t *testing.T) {
	fx, _ := stallFleet(t, "")
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	has(t, "switch", fx.h.ok("supervisor", "switch", "--model", "opus", "--effort", "high"), "switch: pending")
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	fx.h.vars["PATH"] = t.TempDir()
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	fx.h.ok("supervisor", "switch", "--cancel")
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
}

func TestStallOffNeverWakes(t *testing.T) {
	fx, log := stallFleet(t, `,"stall":"off"`)
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	noStall(t, fx)
	if alerts := stallAlerts(log); len(alerts) != 0 {
		t.Fatalf("alerts = %q", alerts)
	}
}

func TestStallFollowsTheConfiguredDelays(t *testing.T) {
	fx, _ := stallFleet(t, `,"stall":{"after":"5m","every":"15m"}`)
	stop := startWatch(t, fx, "--every", "30ms")
	defer stop()
	stallTick(t, fx)
	for i, step := range []struct {
		wait time.Duration
		idle string
	}{{5 * time.Minute, "5m"}, {10 * time.Minute, "15m"}, {15 * time.Minute, "30m"}, {15 * time.Minute, "45m"}} {
		fx.h.advance(step.wait - time.Minute)
		stallTick(t, fx)
		if n := stalls(fx); n != i {
			t.Fatalf("stalls = %d, want %d", n, i)
		}
		fx.h.advance(time.Minute)
		wantStall(t, fx, i+1, "t1 is active, no attempt is running, idle "+step.idle)
	}
}

func TestABadStallSettingIsNamedAndTheDefaultsApply(t *testing.T) {
	fx, _ := stallFleet(t, `,"stall":{"after":"soon"}`)
	stop := startWatch(t, fx, "--every", "30ms")
	stallTick(t, fx)
	fx.h.advance(20 * time.Minute)
	wantStall(t, fx, 1, "t1 is active, no attempt is running, idle 20m")
	out, _ := stop()
	if n := strings.Count(out, "routing.json unreadable, stall wakes use the defaults"); n != 1 || !strings.Contains(out, `stall.after is \"soon\"; give a duration from 1m to 24h`) {
		t.Fatalf("watch out = %q", out)
	}
}
