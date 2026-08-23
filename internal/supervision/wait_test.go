package supervision

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/watcher"
)

func actionableEvidence(id, generation, kind string) orientation.ActionableEvidence {
	return orientation.ActionableEvidence{
		TargetID: id, TargetKind: "task", Generation: []string{generation},
		Kind: kind, Reason: "worker reports blocked", Provenance: "report",
	}
}

func fixedReader(evidence orientation.Evidence) EvidenceReader {
	return func(context.Context) (orientation.Evidence, error) { return evidence, nil }
}

// Replaces the three watcher-boundary seams so tests drive delivery without
// herdr. Counters stay atomic because concurrent waiters share one stub.
type watcherStub struct {
	acquired atomic.Int64
	cycles   atomic.Int64
	onCycle  func(s *watcherStub) error
}

func newWatcherStub(t *testing.T, attached bool, onCycle func(*watcherStub) error) (*watcherStub, func()) {
	t.Helper()
	stub := &watcherStub{onCycle: onCycle}
	if stub.onCycle == nil {
		// Default: every cycle delivers a catch-up whose episodes are all
		// already deduped, so the wait keeps re-arming until its deadline.
		stub.onCycle = func(*watcherStub) error { return nil }
	}
	origAcquire, origRun, origAttached := acquireWatcherOwnership, runWatcherUntilEvent, watcherAttached
	acquireWatcherOwnership = func(context.Context, string, bool) (*watcher.Ownership, error) {
		stub.acquired.Add(1)
		return nil, nil
	}
	runWatcherUntilEvent = func(ctx context.Context, cfg watcher.Config, out, errOut io.Writer) error {
		stub.cycles.Add(1)
		return stub.onCycle(stub)
	}
	watcherAttached = func(string) (bool, error) { return attached, nil }
	return stub, func() {
		acquireWatcherOwnership, runWatcherUntilEvent, watcherAttached = origAcquire, origRun, origAttached
	}
}

func TestWaitReturnsImmediatelyOnAlreadyActionableWork(t *testing.T) {
	_, restore := newWatcherStub(t, false, nil)
	defer restore()

	wake, err := Wait(context.Background(), Waiter{
		Home:         t.TempDir(),
		ReadEvidence: fixedReader(orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}}),
		Ledger:       OpenLedger(t.TempDir()),
	}, WaitConfig{Host: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(wake.Episodes) != 1 || wake.Message == "" || wake.Host != "codex" || wake.FleetID != "f_1" {
		t.Fatalf("wake = %#v, want one coalesced eligible episode with bounded text", wake)
	}
}

// Near-simultaneous subjects become one reasoning opportunity, never one turn
// per worker event.
func TestWaitCoalescesMultipleSubjectsIntoOneWake(t *testing.T) {
	_, restore := newWatcherStub(t, false, nil)
	defer restore()

	wake, err := Wait(context.Background(), Waiter{
		Home: t.TempDir(),
		ReadEvidence: fixedReader(orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
			actionableEvidence("t1", "g1", "blocked"),
			actionableEvidence("t2", "g1", "needs-decision"),
			actionableEvidence("t3", "g1", "failed"),
		}}),
		Ledger: OpenLedger(t.TempDir()),
	}, WaitConfig{Host: "pi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(wake.Episodes) != 3 {
		t.Fatalf("episodes = %d, want all current subjects coalesced into one wake", len(wake.Episodes))
	}
}

// The same unchanged episode must not storm turns; with a bounded deadline the
// wait keeps cycling silently and ends as the caller-requested checkpoint.
func TestSameEpisodeRepeatedlyObservedDoesNotStorm(t *testing.T) {
	reader := fixedReader(orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}})
	ledger := OpenLedger(t.TempDir())
	ctx := context.Background()

	first, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: reader, Ledger: ledger}, WaitConfig{Host: "claude"})
	if err != nil || len(first.Episodes) != 1 {
		t.Fatalf("first wait = %v, %d episodes", err, len(first.Episodes))
	}

	stub, restore := newWatcherStub(t, false, nil)
	defer restore()
	checkpoint, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: reader, Ledger: ledger}, WaitConfig{
		Host:         "claude",
		PollInterval: time.Millisecond,
		Timeout:      80 * time.Millisecond,
	})
	if !errors.Is(err, watcher.ErrNoEvent) || len(checkpoint.Episodes) != 0 {
		t.Fatalf("second wait = %v, %d episodes, want the checkpoint result and no wake", err, len(checkpoint.Episodes))
	}
	if stub.cycles.Load() < 2 {
		t.Fatalf("cycles = %d, want the wait to keep re-arming instead of waking on unchanged currentness", stub.cycles.Load())
	}
}

func TestNewCurrentnessBecomesEligibleAgainAfterOrient(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	ctx := context.Background()
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("t1", "g1", "blocked")}}

	first, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "grok"})
	if err != nil || len(first.Episodes) != 1 {
		t.Fatalf("first wake = %v, %d episodes", err, len(first.Episodes))
	}
	if err := ledger.MarkOriented(first.Episodes); err != nil {
		t.Fatal(err)
	}
	if _, _, eligible, err := currentEpisodes(ctx, Waiter{ReadEvidence: fixedReader(evidence), Ledger: ledger}, "grok"); err != nil || len(eligible) != 0 {
		t.Fatalf("oriented unchanged condition = %d eligible (err %v), want none", len(eligible), err)
	}

	evidence.Actionable[0].Generation = []string{"g2"}
	second, err := Wait(ctx, Waiter{Home: t.TempDir(), ReadEvidence: fixedReader(evidence), Ledger: ledger}, WaitConfig{Host: "grok"})
	if err != nil || len(second.Episodes) != 1 {
		t.Fatalf("changed currentness = %v, %d episodes, want normally eligible", err, len(second.Episodes))
	}
	if second.Episodes[0].Key() == first.Episodes[0].Key() {
		t.Fatal("a delayed stale hint must not be mistaken for fresh truth")
	}
}

// A condition becoming actionable after arming wakes through one delivered
// cycle, recomputed from the authoritative level rather than the event text.
func TestEventAfterArmWakesFromAuthoritativeLevel(t *testing.T) {
	evidence := orientation.Evidence{FleetID: "f_1"}
	stub, restore := newWatcherStub(t, false, func(s *watcherStub) error {
		evidence.Actionable = append(evidence.Actionable, actionableEvidence("t9", "g1", "parked"))
		return nil
	})
	defer restore()

	wake, err := Wait(context.Background(), Waiter{
		Home:         t.TempDir(),
		ReadEvidence: func(context.Context) (orientation.Evidence, error) { return evidence, nil },
		Ledger:       OpenLedger(t.TempDir()),
	}, WaitConfig{Host: "opencode", PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if len(wake.Episodes) != 1 || wake.Episodes[0].TargetID != "t9" || stub.cycles.Load() < 1 {
		t.Fatalf("wake = %#v after %d cycles, want the post-arm condition delivered once", wake, stub.cycles.Load())
	}
}

// With another watcher owning the fleet home, this wait levels on evidence
// without stealing its ownership.
func TestAttachedWatcherDefersToLevelPolling(t *testing.T) {
	var mu sync.Mutex
	evidence := orientation.Evidence{FleetID: "f_1"}
	stub, restore := newWatcherStub(t, true, nil)
	defer restore()

	go func() {
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		evidence.Actionable = append(evidence.Actionable, actionableEvidence("t5", "g1", "blocked"))
	}()

	wake, err := Wait(context.Background(), Waiter{Home: t.TempDir(), ReadEvidence: func(context.Context) (orientation.Evidence, error) {
		mu.Lock()
		defer mu.Unlock()
		return evidence, nil
	}, Ledger: OpenLedger(t.TempDir())}, WaitConfig{Host: "claude", PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if stub.acquired.Load() != 0 {
		t.Fatal("an attached operator watcher keeps its ownership; supervision wait polls instead")
	}
	if len(wake.Episodes) != 1 {
		t.Fatalf("episodes = %d, want the level-poll to observe the new condition", len(wake.Episodes))
	}
}

func TestReplacedAndInterruptedPassThroughTyped(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
	}{
		{"replaced", watcher.ErrReplaced},
		{"interrupted", watcher.ErrInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, restore := newWatcherStub(t, false, func(*watcherStub) error { return test.want })
			defer restore()

			_, err := Wait(context.Background(), Waiter{
				Home:         t.TempDir(),
				ReadEvidence: fixedReader(orientation.Evidence{FleetID: "f_1"}),
				Ledger:       OpenLedger(t.TempDir()),
			}, WaitConfig{Host: "codex"})
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v preserved for the exit taxonomy", err, test.want)
			}
		})
	}
}

// A host accepting a wake whose Supervisor crashes before orient loses only
// mechanism memory: after the progress window the same currentness retries,
// while canonical actionability never left durable state.
func TestAcceptedWithoutOrientRetriesBounded(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	e := Episode{FleetID: "f_1", TargetID: "t1", TargetKind: "task",
		Currentness: orientation.TargetFor("f_1", orientation.TargetEvidence{ID: "t1", Kind: "task", Generation: []string{"g1"}}).Currentness,
		Kind:        "unacknowledged"}

	if err := ledger.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.MarkAccepted(Keys([]Episode{e})); err != nil {
		t.Fatal(err)
	}
	now = now.Add(DefaultPolicy().ProgressTimeout - time.Minute)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 0 {
		t.Fatal("inside the progress window a crash-before-orient is still awaited")
	}
	now = now.Add(2 * DefaultPolicy().ProgressTimeout)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("past the window, bounded recovery makes the same episode eligible again")
	}
}
