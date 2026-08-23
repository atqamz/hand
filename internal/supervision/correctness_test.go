package supervision

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/watcher"
)

// Two concurrent waiters racing on the same exact episode must produce
// exactly one successful claim overall: eligibility and the requested stamp
// happen inside one ledger transaction.
func TestConcurrentWaitersClaimExactlyOnce(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}}
	reader := fixedReader(evidence)
	stub, restore := newWatcherStub(t, false, nil)
	defer restore()

	var total atomic.Int64
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wake, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: reader, Ledger: ledger}, WaitConfig{
				Host:         "opencode",
				PollInterval: time.Millisecond,
				Timeout:      150 * time.Millisecond,
			})
			if err != nil && !errors.Is(err, watcher.ErrNoEvent) {
				t.Errorf("waiter %d: %v", i, err)
				return
			}
			total.Add(int64(len(wake.Episodes)))
		}(i)
	}
	wg.Wait()
	if total.Load() != 1 {
		t.Fatalf("claims = %d across 10 concurrent waiters, want exactly one", total.Load())
	}
	if stub.acquired.Load() > 9 {
		t.Fatalf("ownership acquisitions = %d, want one per losing waiter at most", stub.acquired.Load())
	}
}

// Different exact episodes may coalesce into one wake; changed currentness is
// independently claimable again.
func TestClaimCoalescesDistinctEpisodesAndNewnessReclaims(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
		actionableEvidence("t1", "g1", "blocked"),
		actionableEvidence("t2", "g1", "failed"),
		actionableEvidence("t3", "g1", "parked"),
	}}
	wake, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "pi"})
	if err != nil || len(wake.Episodes) != 3 {
		t.Fatalf("wake = %#v, %v; want three coalesced subjects in ONE wake", wake, err)
	}

	next := evidence
	next.Actionable = []orientation.ActionableEvidence{actionableEvidence("t1", "g2", "blocked")}
	retry, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(next), Ledger: ledger}, WaitConfig{Host: "pi"})
	if err != nil || len(retry.Episodes) != 1 {
		t.Fatalf("changed-currentness wake = %#v, %v; want independently eligible", retry, err)
	}
}

// Sixteen distinct legitimate episodes each wake exactly once - the storm and
// permanent-disable guard for Stop-hook recursion limits.
func TestSixteenDistinctEpisodesEachWakeOnce(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	ctx := context.Background()
	for i := range 16 {
		evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
			actionableEvidence(fmt.Sprintf("t%02d", i), fmt.Sprintf("gen%d", i), "blocked"),
		}}
		wake, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "claude"})
		if err != nil || len(wake.Episodes) != 1 {
			t.Fatalf("episode %d: wake = %#v, %v; want exactly one legitimate rewake", i, wake, err)
		}
		if err := ledger.MarkAccepted(Keys(wake.Episodes)); err != nil {
			t.Fatal(err)
		}
		if err := ledger.MarkOriented(wake.Episodes); err != nil {
			t.Fatal(err)
		}
		// The unchanged episode never re-claims afterwards.
		if again := ledger.ClaimEligible(FromEvidence(evidence)); len(again) != 0 {
			t.Fatalf("episode %d re-claimed after orient: %d", i, len(again))
		}
	}
}

// Attachment comes from this runtime's heartbeat record, watcher liveness from
// fleet-home ownership; neither field may imply the other.
func TestAttachmentIsIndependentFromWatcherLiveness(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if err := WriteAttachment(home, AttachmentRecord{
		Host: "opencode", Runtime: "ses-1", PID: 1, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	status := Status{Harness: harness.OpenCode}
	status.finishIndependentFields(StatusInput{Home: home})
	if status.Attachment != "attached:ses-1" {
		t.Fatalf("attachment = %q, want attached to the live runtime record", status.Attachment)
	}
	if status.WatcherLiveness != "idle" {
		t.Fatalf("liveness = %q on a home no watcher owns; the two fields are independent answers", status.WatcherLiveness)
	}

	expired := home
	status2 := Status{Harness: harness.OpenCode}
	if err := WriteAttachment(expired, AttachmentRecord{
		Host: "opencode", Runtime: "ses-9", PID: 1, FleetID: "f_1",
		StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	status2.finishIndependentFields(StatusInput{Home: expired})
	if status2.Attachment != "detached" {
		t.Fatalf("expired heartbeat reads %q, want detached", status2.Attachment)
	}
}

// A secondary runtime defers instead of stealing a bridge another live
// runtime already holds.
func TestSecondaryRuntimeCannotStealAnAttachedBridge(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if err := WriteAttachment(home, AttachmentRecord{
		Host: "opencode", Runtime: "primary-session", PID: 424242, FleetID: "f_1",
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	evidence := orientation.Evidence{FleetID: "f_1"}
	stub, restore := newWatcherStub(t, true, nil)
	defer restore()

	_, err := Wait(context.Background(), Waiter{
		Home:         home,
		ReadEvidence: func(context.Context) (orientation.Evidence, error) { return evidence, nil },
		Ledger:       OpenLedger(t.TempDir()),
	}, WaitConfig{Host: "opencode", RuntimeSession: "secondary-session", PollInterval: time.Millisecond})
	if !errors.Is(err, ErrBridgeOwned) {
		t.Fatalf("err = %v, want ErrBridgeOwned for the secondary runtime", err)
	}
	if stub.acquired.Load() != 0 {
		t.Fatal("a deferring waiter must not touch watcher ownership either")
	}
}

// Capability honesty: nothing claims supported before live qualification,
// instruction-only bridges included, and non-supervisor harness names are
// outside the registry even if they build workers.
func TestCapabilityVocabularyStaysHonestBeforeLiveQualification(t *testing.T) {
	if qualifiedHosts[harness.Grok] || qualifiedHosts[harness.Codex] {
		t.Fatal("live qualification flags must start empty")
	}
	home := t.TempDir()
	exe := "/opt/bin/hand"
	if _, err := InstallClaudeStopHook(home, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexHooks(home, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostAssets(home, harness.OpenCode, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostAssets(home, harness.Pi, exe); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CodexThreadEnv, "thr_live")
	for _, name := range SupervisorHosts() {
		status, err := IntegrationStatus(context.Background(), StatusInput{Home: home, Detection: harness.Detection{Name: name, Source: "override"}, Exe: exe})
		if err != nil {
			t.Fatal(err)
		}
		if status.WakeDelivery == CapabilitySupported {
			t.Fatalf("%s claimed supported without any live qualification", name)
		}
		if status.WakeDelivery != CapabilityUnqualified && status.WakeDelivery != CapabilityDegraded {
			t.Fatalf("%s = %q with full static integration installed; want available-unqualified or degraded with reason", name, status.WakeDelivery)
		}
	}
	grok, err := IntegrationStatus(context.Background(), StatusInput{Home: home, Detection: harness.Detection{Name: harness.Grok, Source: "override"}, Exe: exe})
	if err != nil {
		t.Fatal(err)
	}
	if grok.Integration != "not-required" && grok.WakeDelivery == CapabilitySupported {
		t.Fatal("grok must not become supported from instructions alone")
	}
}

func TestWorkerCapableNameOutsideRegistryIsRejected(t *testing.T) {
	if IsSupervisorHost("antigravity") {
		t.Fatal("antigravity is worker-execution support only; it must never enter the supervisor registry implicitly")
	}
	for _, name := range []string{"antigravity", "", "claude-code"} {
		if IsSupervisorHost(name) {
			t.Fatalf("%q must not resolve as a supervisor host", name)
		}
	}
}

// Crash after host acceptance but before orient: suppressed through the
// progress window, then bounded recovery makes it eligible again.
func TestAcceptedThenCrashRecoversBounded(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	now := time.Date(2026, 8, 23, 18, 0, 0, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "unacknowledged")}}
	episodes := FromEvidence(evidence)

	claimed := ledger.ClaimEligible(episodes)
	if len(claimed) != 1 {
		t.Fatalf("claim = %d, want the episode", len(claimed))
	}
	if err := ledger.MarkAccepted(Keys(claimed)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultPolicy().ProgressTimeout / 2)
	if eligible := ledger.Eligible(episodes); len(eligible) != 0 {
		t.Fatal("inside the window a crashed delivery is still awaited")
	}
	now = now.Add(2 * DefaultPolicy().ProgressTimeout)
	if eligible := ledger.Eligible(episodes); len(eligible) != 1 {
		t.Fatal("past the window bounded recovery re-eligibles the episode")
	}
}
