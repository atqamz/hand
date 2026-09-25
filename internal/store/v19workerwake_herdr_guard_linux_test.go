//go:build linux

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"syscall"
	"testing"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/osfacts"
	"golang.org/x/sys/unix"
)

type execGuardWakeTest struct {
	*execGuardLaunchTest
	launch CanonicalV19LaunchRequest
	input  CanonicalV19WorkerInput
	root   osfacts.Incarnation
	assoc  string
	client *canonicalV19HerdrWorkerWakeFakeClient
	deps   canonicalV19HerdrWorkerWakeDeps
	wakes  int
}

// A(B) needs a real controlling terminal whose foreground group is R's. atLaunch changes what
// Herdr reports while the Launch records assoc; the wake then sees the true pane again.
func newExecGuardWakeTest(t *testing.T, atLaunch ...func(*herdr.ProcessInfo)) *execGuardWakeTest {
	t.Helper()
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exec sleep 30")
	launchDeps := l.submit(t)
	_, tty := openPTY(t)
	guard := l.guard(t)
	onTerminal(guard, tty)
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.stopGuard(t); _ = guard.Wait() })
	waitExecGuard(t, "the running record", func() bool { _, err := execguard.ReadRecord(l.dir, execguard.KindRunning); return err == nil })
	current, err := readCanonicalV19HerdrLaunchCurrent(context.Background(), l.home, l.input.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	running := mustExecGuardRecord(t, l.dir, execguard.KindRunning)
	base := newCanonicalV19HerdrLaunchFakeClient(t, l.home, current.Current.Request, l.key)
	base.processInfo.TTY, base.processInfo.ForegroundProcessGroupID = tty.Name(), running.ProcessGroup
	pane := base.panes[l.key.PaneID]
	pane.Agent, pane.AgentStatus = "worker", herdr.StatusIdle
	base.panes[l.key.PaneID] = pane
	live := base.processInfo
	for _, change := range atLaunch {
		change(&base.processInfo)
	}
	launchDeps.clientFor = func(string) canonicalV19HerdrLaunchClient { return base }
	if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, launchDeps); state != "succeeded" {
		t.Fatalf("Launch = %q, %v, want an established guarded ExecutorBinding", state, err)
	}
	base.processInfo = live
	_, key, _ := l.binding(t)
	recorded, err := parseCanonicalV19ExecGuardKey(key)
	if err != nil || (len(atLaunch) == 0 && recorded.Assoc != "observed") {
		t.Fatalf("Launch key %q (%v), want assoc observed when Herdr reports the guard's terminal and group", key, err)
	}
	input, err := CreateCanonicalV19WorkerInput(context.Background(), l.home,
		canonicalV19WorkerInputCreateInput(current.Current.Request, "worker-input-guard-wake", "semantic payload must not enter the doorbell", "digest-worker-input-guard-wake"))
	if err != nil {
		t.Fatal(err)
	}
	w := &execGuardWakeTest{
		execGuardLaunchTest: l, launch: current.Current.Request, input: input, root: *running.Root, assoc: recorded.Assoc,
		client: &canonicalV19HerdrWorkerWakeFakeClient{canonicalV19HerdrLaunchFakeClient: base, requireSubmittedAtPrompt: true},
		deps:   canonicalV19HerdrWorkerWakeDefaultDeps(),
	}
	w.deps.clientFor = func(string) canonicalV19HerdrWorkerWakeClient { return w.client }
	w.deps.execGuard = true
	return w
}

func (w *execGuardWakeTest) wake(t *testing.T) (string, string, error) {
	t.Helper()
	w.wakes++
	w.client.wakeOperationID = "operation-guard-wake-" + strconv.Itoa(w.wakes)
	state, err := wakeCanonicalV19Herdr(context.Background(), w.home,
		canonicalV19WorkerWakePrepareInput(w.launch, w.client.wakeOperationID, w.input.Ordinal), w.deps)
	return state, canonicalV19OperationState(t, w.home, w.client.wakeOperationID), err
}

func (w *execGuardWakeTest) endHarness(t *testing.T) {
	t.Helper()
	if err := unix.Kill(w.root.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitExecGuard(t, "the ceased record", func() bool { _, err := execguard.ReadRecord(w.dir, execguard.KindCeased); return err == nil })
}

func canonicalV19OperationState(t *testing.T, home, operationID string) string {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state string
	err = db.sql.QueryRow(`SELECT state FROM external_operation WHERE id=?`, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestExecGuardWakeDeliversOnlyTheDoorbellAndNeverSuppressesALaterWake(t *testing.T) {
	w := newExecGuardWakeTest(t)
	for attempt := 1; attempt <= 2; attempt++ {
		if state, persisted, err := w.wake(t); state != "succeeded" || persisted != "succeeded" || err != nil || w.client.promptCalls != attempt {
			t.Fatalf("wake %d = %q (%q), %v with %d prompts, want succeeded from W(B) before and after one prompt (EG-10)", attempt, state, persisted, err, w.client.promptCalls)
		}
	}
	db, err := openReadOnly(w.home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var acknowledged int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM worker_input_acknowledgement WHERE worker_input_id=?`, w.input.ID).Scan(&acknowledged); err != nil || acknowledged != 0 {
		t.Fatalf("acknowledgements after two wakes = %d, %v, want none: a wake is mechanism-only", acknowledged, err)
	}
}

func TestExecGuardWakeRefusesBeforeThePromptWhenWFails(t *testing.T) {
	w := newExecGuardWakeTest(t)
	for _, test := range []struct {
		name   string
		mutate func(*canonicalV19HerdrWorkerWakeFakeClient) func()
	}{
		{"Herdr reports another pane terminal", func(c *canonicalV19HerdrWorkerWakeFakeClient) func() {
			tty := c.processInfo.TTY
			c.processInfo.TTY = "/dev/null"
			return func() { c.processInfo.TTY = tty }
		}},
		{"Herdr reports no pane terminal (counterexample 17)", func(c *canonicalV19HerdrWorkerWakeFakeClient) func() {
			tty := c.processInfo.TTY
			c.processInfo.TTY = ""
			return func() { c.processInfo.TTY = tty }
		}},
		{"Herdr reports another foreground group", func(c *canonicalV19HerdrWorkerWakeFakeClient) func() {
			group := c.processInfo.ForegroundProcessGroupID
			c.processInfo.ForegroundProcessGroupID = 1
			return func() { c.processInfo.ForegroundProcessGroupID = group }
		}},
		{"the agent is blocked", func(c *canonicalV19HerdrWorkerWakeFakeClient) func() {
			pane := c.panes[c.processInfo.PaneID]
			blocked := pane
			blocked.AgentStatus = herdr.StatusBlocked
			c.panes[c.processInfo.PaneID] = blocked
			return func() { c.panes[c.processInfo.PaneID] = pane }
		}},
		{"the workspace no longer carries the SessionBinding locator", func(c *canonicalV19HerdrWorkerWakeFakeClient) func() {
			label := c.workspaces[0].Label
			c.workspaces[0].Label = "another-session"
			return func() { c.workspaces[0].Label = label }
		}},
	} {
		restore := test.mutate(w.client)
		state, persisted, err := w.wake(t)
		restore()
		if state != "no-effect" || persisted != "no-effect" || err == nil || w.client.promptCalls != 0 {
			t.Fatalf("%s: wake = %q (%q), %v with %d prompts, want no-effect before any prompt (EG-10)", test.name, state, persisted, err, w.client.promptCalls)
		}
	}
	if state, _, err := w.wake(t); state != "succeeded" {
		t.Fatalf("wake after the refusals = %q, %v, want succeeded once W(B) holds again", state, err)
	}
}

func TestExecGuardWakeOfABindingWhoseLaunchDidNotObserveTheAssociationIsRefused(t *testing.T) {
	for _, test := range []struct{ assoc, tty string }{{"unobserved", ""}, {"mismatch", "/dev/null"}} {
		w := newExecGuardWakeTest(t, func(info *herdr.ProcessInfo) { info.TTY = test.tty })
		if w.assoc != test.assoc {
			t.Fatalf("recorded assoc = %q, want %q", w.assoc, test.assoc)
		}
		if state, persisted, err := w.wake(t); state != "no-effect" || persisted != "no-effect" || err == nil || w.client.herdrCalls != 0 {
			t.Fatalf("wake of an assoc=%s binding = %q (%q), %v with %d Herdr calls, want no-effect before any Herdr call although the pane now matches (counterexamples 16, 17)",
				test.assoc, state, persisted, err, w.client.herdrCalls)
		}
	}
}

func TestExecGuardWakeOfAnEndedHarnessIsNoEffect(t *testing.T) {
	w := newExecGuardWakeTest(t)
	w.endHarness(t)
	if state, persisted, err := w.wake(t); state != "no-effect" || persisted != "no-effect" || err == nil || w.client.herdrCalls != 0 {
		t.Fatalf("wake = %q (%q), %v with %d Herdr calls, want no-effect before any Herdr call once R ended (counterexample 5)", state, persisted, err, w.client.herdrCalls)
	}
}

func TestExecGuardWakeHerdrRefusesBeforeInputIsRejected(t *testing.T) {
	w := newExecGuardWakeTest(t)
	w.client.promptErr = &herdr.APIError{Operation: "agent prompt " + w.key.PaneID, Code: "agent_blocked", Message: "agent is blocked"}
	if state, persisted, err := w.wake(t); state != "rejected" || persisted != "rejected" || err == nil {
		t.Fatalf("wake = %q (%q), %v, want rejected from Herdr's pre-side-effect refusal (EG-10)", state, persisted, err)
	}
}

func TestExecGuardWakeWithALostReplyStaysUncertainAndHoldsTheExecutor(t *testing.T) {
	w := newExecGuardWakeTest(t)
	w.client.promptErr = context.DeadlineExceeded
	state, persisted, err := w.wake(t)
	if state != "uncertain" || persisted != "uncertain" || err == nil {
		t.Fatalf("wake = %q (%q), %v, want uncertain after a lost reply", state, persisted, err)
	}
	if state, err := reconcileCanonicalV19HerdrWorkerWake(context.Background(), w.home, w.client.wakeOperationID, w.deps); state != "uncertain" || err == nil {
		t.Fatalf("reconcile = %q, %v, want uncertain without O1 or attestation (counterexample 18)", state, err)
	}
	next := canonicalV19WorkerWakePrepareInput(w.launch, "operation-guard-wake-after-lost-reply", w.input.Ordinal)
	if _, err := PrepareCanonicalV19WorkerWake(context.Background(), w.home, next); !errors.Is(err, ErrCanonicalV19WorkerWakeConflict) {
		t.Fatalf("later wake while uncertain = %v, want the executor-control claim to refuse it", err)
	}
}

func TestExecGuardWakeWhoseHarnessEndsDuringDeliveryIsUncertain(t *testing.T) {
	w := newExecGuardWakeTest(t)
	w.client.onPrompt = func() { w.endHarness(t) }
	if state, persisted, err := w.wake(t); state != "uncertain" || persisted != "uncertain" || err == nil || w.client.promptCalls != 1 {
		t.Fatalf("wake = %q (%q), %v with %d prompts, want uncertain when W(B) fails after delivery (EG-10, counterexample 5)", state, persisted, err, w.client.promptCalls)
	}
}

func TestProductionWakeKeepsTheRevisionOneRefusal(t *testing.T) {
	w := newExecGuardWakeTest(t)
	w.deps.execGuard = false
	state, persisted, err := w.wake(t)
	if state != "" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) || persisted != "absent" || w.client.promptCalls != 0 {
		t.Fatalf("production wake = %q (%q), %v with %d prompts, want the revision-1 refusal before any row until the platform acceptance tests pass", state, persisted, err, w.client.promptCalls)
	}
}

func TestExecGuardWakeOfALegacyBindingKeepsTheRefusal(t *testing.T) {
	fixture, request, _, client, _ := canonicalV19HerdrWorkerWakeFixture(t, "operation-legacy-wake-first")
	deps := canonicalV19HerdrWorkerWakeDefaultDeps()
	deps.clientFor = func(string) canonicalV19HerdrWorkerWakeClient { return client }
	deps.execGuard = true
	if state, _ := reconcileCanonicalV19HerdrWorkerWake(context.Background(), fixture.Home, request.OperationID, deps); state != "no-effect" {
		t.Fatalf("first legacy wake = %q, want no-effect", state)
	}
	input := CanonicalV19WorkerWakePrepareInput{
		OperationID: "operation-legacy-wake-second", OperationKey: "operation-key-legacy-wake-second",
		ExecutorBindingID: request.ExecutorBindingID, PendingThroughOrdinal: request.PendingThroughOrdinal,
		WakeReason: request.WakeReason, CreatedAt: "2026-09-09T06:20:00Z",
	}
	state, err := wakeCanonicalV19Herdr(context.Background(), fixture.Home, input, deps)
	if state != "no-effect" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) || client.promptCalls != 0 {
		t.Fatalf("legacy-key wake = %q, %v with %d prompts, want the revision-1 refusal for a key without a guard grammar", state, err, client.promptCalls)
	}
}
