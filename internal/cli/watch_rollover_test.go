package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
)

type rolloverFixture struct {
	*attemptFixture
	notify string
}

func newRolloverFixture(t *testing.T, supervisor string) *rolloverFixture {
	t.Helper()
	fx := newAttemptFixture(t)
	notify := filepath.Join(t.TempDir(), "notify.log")
	fakebin.Install(t, fx.h.vars["PATH"], "hand-notify", "notifyenv", map[string]string{"log": notify})
	policy := `{"notify":["hand-notify"],"stall":"off"` + supervisor + `}`
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	setWait(t, cli.RolloverWait, 5*time.Second)
	setWait(t, cli.RolloverPoll, 10*time.Millisecond)
	setWait(t, cli.RolloverStill, 10*time.Minute)
	startClaudeSupervisor(fx.h)
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	return &rolloverFixture{fx, notify}
}

func (fx *rolloverFixture) session() string {
	fx.h.t.Helper()
	m := regexp.MustCompile(`session: (\S+)`).FindStringSubmatch(fx.h.ok("supervisor", "show"))
	if m == nil {
		fx.h.t.Fatal("supervisor show names no session")
	}
	return m[1]
}

func writeContext(home, session string, tokens int) error {
	path := filepath.Join(home, ".claude", "projects", "p", session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line := `{"type":"assistant","timestamp":"2026-09-26T00:00:01Z","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":` + strconv.Itoa(tokens) + `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}` + "\n"
	return os.WriteFile(path, []byte(line), 0o644)
}

func (fx *rolloverFixture) context(session string, tokens int) {
	fx.h.t.Helper()
	if err := writeContext(fx.h.vars["HOME"], session, tokens); err != nil {
		fx.h.t.Fatal(err)
	}
}

func (fx *rolloverFixture) freshSessionsHold(tokens int) {
	home := fx.h.vars["HOME"]
	fx.rt.set(func(rt *fakeRuntime) {
		rt.onCreate = func(call createCall) {
			for i, a := range call.Command {
				if a == "--session-id" && i+1 < len(call.Command) {
					_ = writeContext(home, call.Command[i+1], tokens)
				}
			}
		}
	})
}

func (fx *rolloverFixture) writesNote() {
	note := filepath.Join(fx.h.home, "memory", "handoff.md")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.onPrompt = func() {
			if p := rt.sent[len(rt.sent)-1]; strings.HasPrefix(p, "[hand v1 rollover]") {
				_ = os.WriteFile(note, []byte(fmt.Sprintf("# Handoff\nt1: wait for CI %d\n", len(rt.sent))), 0o644)
			}
		}
	})
}

func (fx *rolloverFixture) asked() int {
	n := 0
	for _, p := range fx.rt.prompts() {
		if strings.HasPrefix(p, "[hand v1 rollover]") {
			n++
		}
	}
	return n
}

func (fx *rolloverFixture) askTries() int {
	n := 0
	for _, call := range fx.rt.srv.Calls("agent.prompt") {
		if strings.Contains(string(call), "[hand v1 rollover]") {
			n++
		}
	}
	return n
}

func (fx *rolloverFixture) idle(t *testing.T) {
	t.Helper()
	want := explains(fx.rt) + 8
	eventually(t, func() bool { return explains(fx.rt) >= want })
}

func (fx *rolloverFixture) supervisors(t *testing.T) []state.Supervisor {
	t.Helper()
	st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), fx.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sups, err := st.Supervisors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sups
}

func (fx *rolloverFixture) rolledOver(t *testing.T) []string {
	t.Helper()
	st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), fx.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.RecentEventsOf(context.Background(), []string{"supervisor.rolled_over"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func rolloverAlerts(log string) []string {
	b, _ := os.ReadFile(log)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "supervisor.rollover|") {
			out = append(out, line)
		}
	}
	return out
}

func TestRolloverStartsAFreshSessionOnceTheSupervisorIsOverTheLimitAndStill(t *testing.T) {
	fx := newRolloverFixture(t, "")
	old := fx.session()
	fx.context(old, 350000)
	fx.writesNote()
	stop := startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	if n := fx.asked(); n != 0 {
		t.Fatalf("asked for a handoff note %d times before the supervisor was still", n)
	}
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
	stop()

	if n := fx.asked(); n != 1 {
		t.Fatalf("asked for a handoff note %d times", n)
	}
	var ask string
	for _, p := range fx.rt.prompts() {
		if strings.HasPrefix(p, "[hand v1 rollover]") {
			ask = p
		}
	}
	note := filepath.Join(fx.h.home, "memory", "handoff.md")
	has(t, "request", ask, "350k tokens", "300k", note, "`done`")
	sups := fx.supervisors(t)
	if sups[0].Status != state.AttemptStopped || !sups[1].Live() || sups[1].Harness != "claude" || sups[1].Model != "sonnet" || sups[1].Effort != "low" || sups[1].Session == old {
		t.Fatalf("supervisors = %+v", sups)
	}
	first := fx.rt.lastCreate().Command
	prompt := first[len(first)-1]
	has(t, "first message", prompt, "run `hand orient` now", "You continue s1, which Hand ended at 350k tokens of context: run the full `hand orient`, then read `"+note+"`")
	events := fx.rolledOver(t)
	if len(events) != 1 || events[0] != "s2: continues s1 (session "+old+", 350000 tokens); new session "+sups[1].Session {
		t.Fatalf("events = %q", events)
	}
}

func TestRolloverDoesNothingUnderTheLimitBusyOrWhenTurnedOff(t *testing.T) {
	for _, c := range []struct {
		name, policy string
		tokens       int
		status       string
	}{
		{"under the limit", "", 299999, "idle"},
		{"busy", "", 350000, "working"},
		{"turned off", `,"supervisor":{"rollover_tokens":0}`, 5000000, "idle"},
		{"a lower limit not reached", `,"supervisor":{"rollover_tokens":400000}`, 350000, "idle"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newRolloverFixture(t, c.policy)
			fx.context(fx.session(), c.tokens)
			fx.rt.set(func(rt *fakeRuntime) { rt.status = c.status })
			startWatch(t, fx.attemptFixture, "--every", "30ms")
			fx.idle(t)
			fx.h.advance(time.Hour)
			fx.idle(t)
			if n, sups := fx.asked(), fx.supervisors(t); n != 0 || len(sups) != 1 || !sups[0].Live() {
				t.Fatalf("asked %d times, supervisors = %+v", n, sups)
			}
		})
	}
}

func TestRolloverUsesTheConfiguredLimit(t *testing.T) {
	fx := newRolloverFixture(t, `,"supervisor":{"rollover_tokens":1000}`)
	fx.context(fx.session(), 1500)
	fx.writesNote()
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.supervisors(t)) == 2 })
	has(t, "request", fx.rt.prompts()[0], "1k tokens", "1k Hand keeps")
}

func TestRolloverKeepsTheSupervisorAndAlertsOnceWithoutANote(t *testing.T) {
	fx := newRolloverFixture(t, "")
	setWait(t, cli.RolloverWait, 300*time.Millisecond)
	fx.context(fx.session(), 350000)
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(rolloverAlerts(fx.notify)) == 1 })
	fx.idle(t)
	fx.h.advance(2 * time.Hour)
	fx.idle(t)
	if n, sups, alerts := fx.asked(), fx.supervisors(t), rolloverAlerts(fx.notify); n != 1 || len(sups) != 1 || !sups[0].Live() || len(alerts) != 1 || !strings.Contains(alerts[0], "s1 holds 350k tokens, over the rollover limit of 300k, and it wrote no handoff note in 300ms") {
		t.Fatalf("asked %d times, supervisors = %+v, alerts = %q", n, sups, alerts)
	}
	if got := fx.rolledOver(t); len(got) != 0 {
		t.Fatalf("events = %q", got)
	}
}

func TestRolloverIgnoresAnOldNoteAndWaitsForANewOne(t *testing.T) {
	fx := newRolloverFixture(t, "")
	setWait(t, cli.RolloverWait, 300*time.Millisecond)
	note := filepath.Join(fx.h.home, "memory", "handoff.md")
	if err := os.WriteFile(note, []byte("# old handoff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.context(fx.session(), 350000)
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(rolloverAlerts(fx.notify)) == 1 })
	if sups := fx.supervisors(t); len(sups) != 1 || !sups[0].Live() {
		t.Fatalf("supervisors = %+v", sups)
	}
}

func TestRolloverWaitsForAnUpdate(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	lock, err := os.OpenFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if ok, err := flock.Lock(lock, false); !ok || err != nil {
		t.Fatalf("lock = %v, %v", ok, err)
	}
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.idle(t)
	if n := fx.asked(); n != 0 {
		t.Fatalf("asked %d times while hand update ran", n)
	}
	if err := flock.Release(lock); err != nil {
		t.Fatal(err)
	}
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.supervisors(t)) == 2 })
}

func TestRolloverWaitsForAQueuedMessage(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	fx.rt.set(func(rt *fakeRuntime) { rt.ready = false })
	fx.h.ok("supervisor", "send", "--text", "operator words")
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.idle(t)
	if n := fx.askTries(); n != 0 {
		t.Fatalf("tried to send %d prompts while a message waited", n)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.ready = true })
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
	prompts := fx.rt.prompts()
	if len(prompts) < 2 || prompts[0] != "operator words" {
		t.Fatalf("prompts = %q", prompts)
	}
}

func (fx *rolloverFixture) duringAsk(act func(rt *fakeRuntime, st *state.Store)) {
	st := openStore(fx.h.t, fx.h)
	note := filepath.Join(fx.h.home, "memory", "handoff.md")
	fx.rt.set(func(rt *fakeRuntime) {
		rt.onPrompt = func() {
			if p := rt.sent[len(rt.sent)-1]; strings.HasPrefix(p, "[hand v1 rollover]") {
				_ = os.WriteFile(note, []byte(fmt.Sprintf("# Handoff\nt1: wait for CI %d\n", len(rt.sent))), 0o644)
				act(rt, st)
			}
		}
	})
}

func (fx *rolloverFixture) alertedOnce(t *testing.T, want ...string) {
	t.Helper()
	eventually(t, func() bool { return len(rolloverAlerts(fx.notify)) >= 1 })
	fx.idle(t)
	alerts := rolloverAlerts(fx.notify)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %q", alerts)
	}
	has(t, "alert", alerts[0], want...)
}

func TestRolloverSendsAMessageAndAWakeFromTheExchangeToTheFreshSession(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.duringAsk(func(_ *fakeRuntime, st *state.Store) {
		ctx := context.Background()
		_, _ = st.AddSupervisorInput(ctx, "operator words")
		_ = st.NoteSupervisor(ctx, 1, "stalled", "new wake")
	})
	if err := openStore(t, fx.h).NoteSupervisor(context.Background(), 1, "stalled", "old wake"); err != nil {
		t.Fatal(err)
	}
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	eventually(t, func() bool { return strings.Contains(strings.Join(fx.rt.prompts(), "\n"), "old wake") })
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
	eventually(t, func() bool {
		all := strings.Join(fx.rt.prompts(), "\n")
		return strings.Contains(all, "operator words") && strings.Contains(all, "new wake")
	})
	fx.idle(t)
	if n, alerts := fx.asked(), rolloverAlerts(fx.notify); n != 1 || len(alerts) != 0 {
		t.Fatalf("asked %d times, alerts = %q", n, alerts)
	}
	if got := strings.Count(strings.Join(fx.rt.prompts(), "\n"), "old wake"); got != 1 {
		t.Fatalf("the old wake reached a session %d times", got)
	}
	sups := fx.supervisors(t)
	if len(sups) != 2 || sups[1].WakeCursor <= sups[0].WakeCursor {
		t.Fatalf("supervisors = %+v", sups)
	}
}

func TestRolloverWaitsForAQueuedWake(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	fx.rt.set(func(rt *fakeRuntime) { rt.ready = false })
	if err := openStore(t, fx.h).NoteSupervisor(context.Background(), 1, "stalled", "waited too long"); err != nil {
		t.Fatal(err)
	}
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.idle(t)
	if n := fx.askTries(); n != 0 {
		t.Fatalf("tried to send %d prompts while a wake waited", n)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.ready = true })
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
}

func TestRolloverWaitsWhileTheScreenKeepsChanging(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	revisions := make([]int64, 5000)
	for i := range revisions {
		revisions[i] = int64(i + 1)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revisions = revisions })
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.idle(t)
	if n := fx.asked(); n != 0 {
		t.Fatalf("asked %d times while the screen changed", n)
	}
}

func TestRolloverStartsNothingAgainWithinTheCooldown(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.freshSessionsHold(350000)
	fx.writesNote()
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
	fx.idle(t)
	fx.h.advance(11 * time.Minute)
	fx.idle(t)
	if n, sups := fx.asked(), fx.supervisors(t); n != 1 || len(sups) != 2 {
		t.Fatalf("asked %d times within the cooldown, supervisors = %+v", n, sups)
	}
	fx.h.advance(time.Hour)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 2 })
}

func TestRolloverKeepsTheSupervisorWhenTheBoardGenerationIsStale(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	writeGeneration(t, fx.h, "dead 1")
	fx.h.vars["HAND_GENERATION"] = "dead 1"
	writeGeneration(t, fx.h, "live 2")
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.alertedOnce(t, "s1 holds 350k tokens", "nothing was stopped", "will not ask it again")
	fx.h.advance(2 * time.Hour)
	fx.idle(t)
	if n, sups := fx.asked(), fx.supervisors(t); n != 1 || len(sups) != 1 || !sups[0].Live() || len(rolloverAlerts(fx.notify)) != 1 {
		t.Fatalf("asked %d times, supervisors = %+v", n, sups)
	}
}

func TestRolloverAsksNothingWhenTheHarnessIsGone(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	if err := os.Remove(filepath.Join(fx.h.vars["PATH"], exe("", "claude"))); err != nil {
		t.Fatal(err)
	}
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.alertedOnce(t, "s1 holds 350k tokens", "Hand cannot start a fresh session", "will not ask it again")
	if n, sups := fx.asked(), fx.supervisors(t); n != 0 || len(sups) != 1 || !sups[0].Live() {
		t.Fatalf("asked %d times, supervisors = %+v", n, sups)
	}
}

func TestRolloverResumesTheOldSessionWhenTheFreshOneCannotStart(t *testing.T) {
	fx := newRolloverFixture(t, "")
	old := fx.session()
	fx.context(old, 350000)
	fx.duringAsk(func(rt *fakeRuntime, _ *state.Store) { rt.createFail, rt.createFails = "create_refused", 1 })
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	fx.alertedOnce(t, "s1 wrote its handoff note", "the fresh session did not start", "resumed s1 as s3", "will not ask it again")
	fx.h.advance(2 * time.Hour)
	fx.idle(t)
	sups := fx.supervisors(t)
	if len(sups) != 3 || sups[0].Status != state.AttemptStopped || sups[1].Status != state.AttemptFailed || !sups[2].Live() || sups[2].Session != old {
		t.Fatalf("supervisors = %+v", sups)
	}
	if n := fx.asked(); n != 1 {
		t.Fatalf("asked %d times", n)
	}
}

func TestRolloverStandsDownForASwitchRequestedDuringTheExchange(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.duringAsk(func(_ *fakeRuntime, st *state.Store) {
		_, _ = st.SetSupervisorSwitch(context.Background(), 1, "opus", "high")
	})
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.supervisors(t)) == 2 })
	fx.idle(t)
	sups := fx.supervisors(t)
	if n := fx.asked(); n != 1 || len(fx.rolledOver(t)) != 0 || sups[1].Model != "opus" || sups[1].Effort != "high" || sups[1].Session != sups[0].Session {
		t.Fatalf("asked %d times, supervisors = %+v", n, sups)
	}
}

func TestRolloverRetriesAnAskThatFailsTwice(t *testing.T) {
	fx := newRolloverFixture(t, "")
	fx.context(fx.session(), 350000)
	fx.writesNote()
	fx.rt.set(func(rt *fakeRuntime) { rt.promptFail, rt.promptFails = "agent_busy", 2 })
	startWatch(t, fx.attemptFixture, "--every", "30ms")
	fx.idle(t)
	fx.h.advance(10 * time.Minute)
	eventually(t, func() bool { return len(fx.rolledOver(t)) == 1 })
	if n := len(rolloverAlerts(fx.notify)); n != 0 {
		t.Fatalf("alerts = %d", n)
	}
}
