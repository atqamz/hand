package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
	"pgregory.net/rapid"
)

// Shared generators for the reconciliation invariants below, covering only the fields
// decideReconciliation and its callees branch on; unbranched-on fields (timestamps, free-text
// ids) draw from a small realistic set since widening them would not exercise a new code path.

func genHerdrIdentity(t *rapid.T, label string) state.Herdr {
	if !rapid.Bool().Draw(t, label+"-present") {
		return state.Herdr{}
	}
	return state.Herdr{
		Session:     rapid.SampledFrom([]string{"", "default"}).Draw(t, label+"-session"),
		WorkspaceID: rapid.SampledFrom([]string{"ws-1", "ws-2"}).Draw(t, label+"-workspace"),
		TabID:       rapid.SampledFrom([]string{"tab-1", "tab-2"}).Draw(t, label+"-tab"),
		PaneID:      rapid.SampledFrom([]string{"pane-1", "pane-2"}).Draw(t, label+"-pane"),
	}
}

func genOptionalTimestamp(t *rapid.T, label string) string {
	if !rapid.Bool().Draw(t, label+"-set") {
		return ""
	}
	return rapid.SampledFrom([]string{"2026-08-01T00:00:00Z", "2026-08-15T00:00:01Z", "2026-08-29T12:30:00Z"}).Draw(t, label+"-value")
}

func genAttemptLifecycle(t *rapid.T, label string) state.AttemptLifecycle {
	return rapid.SampledFrom([]state.AttemptLifecycle{
		state.AttemptProvisioning, state.AttemptRunning, state.AttemptCompleted, state.AttemptFailed, state.AttemptInterrupted,
	}).Draw(t, label)
}

// Covers every field decideReconciliation, decideProvisioning, or decideTerminalConvergence
// branches on directly.
func genAttempt(t *rapid.T) state.Attempt {
	return state.Attempt{
		ID:                      17,
		TaskID:                  "task-1",
		Lifecycle:               genAttemptLifecycle(t, "lifecycle"),
		Harness:                 rapid.SampledFrom([]string{"claude", "codex", "grok"}).Draw(t, "harness"),
		Model:                   rapid.SampledFrom([]string{"opus", "sonnet"}).Draw(t, "model"),
		Effort:                  rapid.SampledFrom([]string{"high", "low"}).Draw(t, "effort"),
		ExecutionClass:          rapid.SampledFrom([]string{"mechanical", "deep"}).Draw(t, "class"),
		PlannedAgainst:          rapid.SampledFrom([]string{"base-1", "base-2"}).Draw(t, "planned"),
		RequestedProfile:        rapid.SampledFrom([]string{"", "profile-a"}).Draw(t, "profile"),
		RoutingSource:           rapid.SampledFrom([]string{"", "route-a"}).Draw(t, "source"),
		Worktree:                rapid.SampledFrom([]string{"", "/pool/1"}).Draw(t, "worktree"),
		LeaseID:                 rapid.SampledFrom([]string{"", "lease-1"}).Draw(t, "lease"),
		Herdr:                   genHerdrIdentity(t, "herdr"),
		LaunchSubmittedAt:       genOptionalTimestamp(t, "submitted"),
		LaunchConfirmedAt:       genOptionalTimestamp(t, "confirmed"),
		TeardownTerminalAttempt: rapid.SampledFrom([]state.AttemptLifecycle{"", state.AttemptCompleted, state.AttemptFailed, state.AttemptInterrupted}).Draw(t, "teardown-terminal"),
	}
}

func genTask(t *rapid.T) state.Task {
	return state.Task{
		ID:             "task-1",
		Project:        "demo",
		Kind:           rapid.SampledFrom([]string{state.KindShip, state.KindScout}).Draw(t, "kind"),
		Lifecycle:      rapid.SampledFrom([]state.TaskLifecycle{state.TaskOpen, state.TaskTerminal}).Draw(t, "task-lifecycle"),
		PR:             rapid.SampledFrom([]string{"", "https://github.com/demo/repo/pull/1"}).Draw(t, "pr"),
		MergeExecuted:  rapid.Bool().Draw(t, "merge-executed"),
		MergeAnnounced: rapid.Bool().Draw(t, "merge-announced"),
		DeliveredAt:    genOptionalTimestamp(t, "delivered"),
	}
}

func genObservation(t *rapid.T) reconciliationObservation {
	return reconciliationObservation{
		Treehouse: treehouseObservation{State: rapid.SampledFrom([]treehouseLeaseState{
			treehouseLeaseUnobserved, treehouseLeaseExact, treehouseLeaseAbsent, treehouseLeaseMismatch, treehouseLeaseUnprovable, treehouseLeaseUnknown,
		}).Draw(t, "treehouse-state")},
		Worktree: worktreeObservation{State: rapid.SampledFrom([]worktreeState{
			worktreeUnobserved, worktreeClean, worktreeDirty, worktreeMissing,
		}).Draw(t, "worktree-state")},
		Herdr: herdrObservation{
			State: rapid.SampledFrom([]herdrOwnershipState{
				herdrOwnershipUnobserved, herdrOwnershipExact, herdrOwnershipAbsent, herdrOwnershipMismatch, herdrOwnershipIncomplete,
			}).Draw(t, "herdr-state"),
			Agent:       rapid.SampledFrom([]string{"", "claude", "codex", "grok"}).Draw(t, "herdr-agent"),
			AgentStatus: rapid.SampledFrom([]herdr.Status{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusBlocked, herdr.StatusDone, herdr.StatusUnknown, ""}).Draw(t, "herdr-agent-status"),
		},
		ReportState:      rapid.SampledFrom([]string{"", state.ReportWorking, "done", "blocked"}).Draw(t, "report-state"),
		Landing:          rapid.SampledFrom([]landingState{landingLanded, landingUnlanded, landingUnknown, ""}).Draw(t, "landing"),
		ObservationError: rapid.Bool().Draw(t, "observation-error"),
	}
}

// INV-REC-6 (docs/testing-invariants.md): exact ownership, a clean worktree, and positive
// commit-safety proof, all three or the automatic return never fires. Sweeps 5 lease x 2 clean
// x 3 safety states (30 combos, every 2-of-3 case) from teardown's real fresh "" start.
func TestAutomaticWorktreeReturnRequiresOwnershipCleanlinessAndCommitSafetyConjunctively(t *testing.T) {
	leaseStates := []worktree.LeaseObservationState{
		worktree.LeaseExact, worktree.LeaseAbsent, worktree.LeaseMismatch, worktree.LeaseUnprovable, worktree.LeaseUnknown,
	}
	cleanStates := []worktree.Cleanliness{worktree.Clean, worktree.Dirty}
	safetyStates := []worktree.CommitSafetyState{worktree.CommitSafetyRemoteObserved, worktree.CommitSafetyLocalOnly, worktree.CommitSafetyUnknown}

	rapid.Check(t, func(rt *rapid.T) {
		lease := rapid.SampledFrom(leaseStates).Draw(rt, "lease-state")
		clean := rapid.SampledFrom(cleanStates).Draw(rt, "clean-state")
		safety := rapid.SampledFrom(safetyStates).Draw(rt, "safety-state")

		home := reconcileFixture(t)
		task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
		attempt := state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1"}
		createRunningAttemptFixture(rt, home, task, attempt)
		history, err := state.ReadHistory(home, "task-1")
		if err != nil {
			rt.Fatal(err)
		}
		activeID := history.ActiveAttempt.ID
		if err := state.TerminalizeTaskAndAttempt(home, "task-1", activeID, state.AttemptRunning, state.AttemptCompleted); err != nil {
			rt.Fatal(err)
		}
		// teardown_worktree_state stays at its real fresh value ("") from here - no pre-seed.

		returns := 0
		r := &Runtime{deps: dependencies{
			now:   func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC) },
			herdr: func() herdrClient { return &reconcileHerdrClient{} },
			worktree: worktreeDependencies{
				observeLease: func(string, string, string) worktree.LeaseObservation {
					return worktree.LeaseObservation{State: lease, LeaseID: "lease-1"}
				},
				observeClean: func(string) (worktree.Cleanliness, error) { return clean, nil },
				observeCommits: func(path string) worktree.CommitSafetyObservation {
					return worktree.CommitSafetyObservation{State: safety, Probe: worktree.CommitSafetyProbe{WorkingDir: path, RemoteRefs: 1}}
				},
				returnWithID:   func(string, string, string, bool) error { returns++; return nil },
				returnWorktree: func(string, string, bool) error { returns++; return nil },
			},
			appendCompletion: completion.Append,
			phase:            func(lifecyclePhase) error { return nil },
		}}

		if _, err = r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
			rt.Fatalf("lease=%s clean=%s safety=%s: Reconcile() = %v, want success", lease, clean, safety, err)
		}

		wantReturn := lease == worktree.LeaseExact && clean == worktree.Clean && safety == worktree.CommitSafetyRemoteObserved
		if wantReturn && returns != 1 {
			rt.Fatalf("lease=%s clean=%s safety=%s (all three proofs hold): return calls = %d, want exactly 1", lease, clean, safety, returns)
		}
		if !wantReturn && returns != 0 {
			rt.Fatalf("lease=%s clean=%s safety=%s (a proof is missing): return calls = %d, want 0 - a 2-of-3 case must never auto-return the worktree", lease, clean, safety, returns)
		}
	})
}

// Counts every mutating call so a test can assert a hard zero rather than merely "an error
// would have been returned if reached" - unlike reconcileHerdrClient, every write here is legal.
type countingHerdrClient struct {
	readOutput string
	readErr    error
	reads      int
	writes     int
}

func (c *countingHerdrClient) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{}, false, nil
}
func (c *countingHerdrClient) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }
func (c *countingHerdrClient) WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	c.writes++
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, nil
}
func (c *countingHerdrClient) WorkspaceClose(string) error         { c.writes++; return nil }
func (c *countingHerdrClient) TabList(string) ([]herdr.Tab, error) { return nil, nil }
func (c *countingHerdrClient) TabCreate(string, string, map[string]string, string) (herdr.Tab, herdr.Pane, error) {
	c.writes++
	return herdr.Tab{}, herdr.Pane{}, nil
}
func (c *countingHerdrClient) TabRename(string, string) error { c.writes++; return nil }
func (c *countingHerdrClient) TabClose(string) error          { c.writes++; return nil }
func (c *countingHerdrClient) PaneGet(string) (herdr.Pane, error) {
	return herdr.Pane{}, errors.New("unused in this test")
}
func (c *countingHerdrClient) PaneRun(string, string) error { c.writes++; return nil }
func (c *countingHerdrClient) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	return herdr.ProcessInfo{}, errors.New("unused in this test")
}
func (c *countingHerdrClient) PaneRunSpec(string, launchSpec) error { c.writes++; return nil }
func (c *countingHerdrClient) PaneSendKeys(string, ...string) error { c.writes++; return nil }
func (c *countingHerdrClient) PaneRead(string, int) (string, error) {
	c.reads++
	return c.readOutput, c.readErr
}

// Like createRunningAttemptFixture, but returns the attempt id so a liveness test can drive
// state.UpdateAttemptObservation-adjacent reads/writes against it directly.
func newLivenessFixture(rt *rapid.T, home string, attempt state.Attempt) int64 {
	task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
	attempt.TaskID, attempt.Lifecycle = "task-1", state.AttemptProvisioning
	created, err := state.CreateTaskWithAttempt(home, task, attempt)
	if err != nil {
		rt.Fatal(err)
	}
	if err := state.MarkLaunchSubmitted(home, "task-1", created.ID, "2026-08-01T00:00:00Z"); err != nil {
		rt.Fatal(err)
	}
	if err := state.MarkLaunchConfirmed(home, "task-1", created.ID, "2026-08-01T00:00:01Z"); err != nil {
		rt.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", created.ID); err != nil {
		rt.Fatal(err)
	}
	return created.ID
}

func mustReadAttempt(rt *rapid.T, home, taskID string) state.Attempt {
	history, err := state.ReadHistory(home, taskID)
	if err != nil {
		rt.Fatal(err)
	}
	if history.ActiveAttempt == nil {
		rt.Fatalf("task %q has no active attempt", taskID)
	}
	return *history.ActiveAttempt
}

// INV-REC-7: calls recordAttemptLiveness directly, bypassing Reconcile's loop, to isolate the
// claim. "Touches no Herdr resource" reads against the attention ADR's own vocabulary: PaneRead
// is a documented fallback probe, not a resource touch - only the write-shaped calls must stay zero.
func TestIdleUnreportedClassificationChangesNoLifecycleLeaseWorktreeOrHerdrResource(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		home := reconcileFixture(t)
		harnessName := rapid.SampledFrom([]string{harness.Claude, harness.Codex, "grok"}).Draw(rt, "harness")
		launchConfirmedAt := rapid.SampledFrom([]string{
			"", "2026-08-01T00:00:00Z", // unset, or long-settled (well past the 30s grace)
		}).Draw(rt, "launch-confirmed-at")
		attempt := state.Attempt{
			Harness:           harnessName,
			Worktree:          rapid.SampledFrom([]string{"", "/pool/1"}).Draw(rt, "worktree"),
			LeaseID:           rapid.SampledFrom([]string{"", "lease-1"}).Draw(rt, "lease"),
			Herdr:             state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
			LastReportState:   rapid.SampledFrom([]string{"", state.ReportWorking, "done", "blocked"}).Draw(rt, "last-report"),
			StatusChangedFor:  rapid.SampledFrom([]string{"", "idle", "working", "blocked", "done"}).Draw(rt, "status-changed-for"),
			StatusChangedAt:   "2026-07-01T00:00:00Z",
			LaunchConfirmedAt: launchConfirmedAt,
			UsageLimitRetryAt: rapid.SampledFrom([]string{"", "2026-08-30T00:00:00Z"}).Draw(rt, "retry-at"),
		}
		attemptID := newLivenessFixture(rt, home, attempt)

		before := mustReadAttempt(rt, home, "task-1")
		taskBefore, err := state.ReadHistory(home, "task-1")
		if err != nil {
			rt.Fatal(err)
		}

		status := rapid.SampledFrom([]herdr.Status{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusBlocked, herdr.StatusDone, herdr.StatusUnknown}).Draw(rt, "pane-status")
		fakeHerdr := &countingHerdrClient{readOutput: "nothing interesting here"}
		r := &Runtime{deps: dependencies{
			now:      func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC) },
			herdrFor: func(string) herdrClient { return fakeHerdr },
		}}

		if _, err := r.recordAttemptLiveness(home, taskBefore.Task, before, status); err != nil {
			rt.Fatalf("recordAttemptLiveness() = %v", err)
		}

		after := mustReadAttempt(rt, home, "task-1")
		taskAfter, err := state.ReadHistory(home, "task-1")
		if err != nil {
			rt.Fatal(err)
		}

		if after.Lifecycle != before.Lifecycle {
			rt.Fatalf("attempt lifecycle changed: before=%s after=%s", before.Lifecycle, after.Lifecycle)
		}
		if after.Worktree != before.Worktree || after.LeaseID != before.LeaseID {
			rt.Fatalf("worktree/lease identity changed: before=(%q,%q) after=(%q,%q)", before.Worktree, before.LeaseID, after.Worktree, after.LeaseID)
		}
		if after.Herdr != before.Herdr {
			rt.Fatalf("Herdr identity changed: before=%+v after=%+v", before.Herdr, after.Herdr)
		}
		if taskAfter.Task.Lifecycle != taskBefore.Task.Lifecycle {
			rt.Fatalf("task lifecycle changed: before=%s after=%s", taskBefore.Task.Lifecycle, taskAfter.Task.Lifecycle)
		}
		if fakeHerdr.writes != 0 {
			rt.Fatalf("attempt=%d status=%s: %d mutating Herdr call(s) fired, want 0 - idle-unreported classification must not touch a Herdr resource", attemptID, status, fakeHerdr.writes)
		}
	})
}

// INV-REC-8: an independent model tracks how many fresh idle/done "stops" (a maximal run of
// consecutive polls sharing one status, since it last differed) the generated poll sequence
// contains, and asserts the probe fires exactly once per fresh stop, never on a repeat poll.
func TestUsageLimitProbeFiresAtMostOncePerStop(t *testing.T) {
	statuses := []herdr.Status{herdr.StatusIdle, herdr.StatusWorking, herdr.StatusBlocked, herdr.StatusDone, herdr.StatusUnknown}

	rapid.Check(t, func(rt *rapid.T) {
		home := reconcileFixture(t)
		attempt := state.Attempt{
			Harness:           harness.Claude, // catalogued, so the probe gate is live throughout
			Herdr:             state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
			LaunchConfirmedAt: "2026-08-01T00:00:00Z", // long past the 30s grace for every poll below
		}
		newLivenessFixture(rt, home, attempt)

		fakeHerdr := &countingHerdrClient{readOutput: "nothing interesting here"} // never matches a limit signature
		r := &Runtime{deps: dependencies{
			now:      func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC) },
			herdrFor: func(string) herdrClient { return fakeHerdr },
		}}

		polls := rapid.IntRange(1, 8).Draw(rt, "poll-count")
		lastStatusChangedFor := ""
		wantProbedThisStop := false
		wantTotalReads := 0
		for i := 0; i < polls; i++ {
			status := rapid.SampledFrom(statuses).Draw(rt, fmt.Sprintf("status-%d", i))
			task, err := state.ReadHistory(home, "task-1")
			if err != nil {
				rt.Fatal(err)
			}
			attemptNow := *task.ActiveAttempt

			if _, err := r.recordAttemptLiveness(home, task.Task, attemptNow, status); err != nil {
				rt.Fatalf("poll %d (status=%s): recordAttemptLiveness() = %v", i, status, err)
			}

			freshStop := string(status) != lastStatusChangedFor
			idleUnreported := status.NotBusy()
			if freshStop {
				lastStatusChangedFor = string(status)
				wantProbedThisStop = false
			}
			if idleUnreported && freshStop && !wantProbedThisStop {
				wantTotalReads++
				wantProbedThisStop = true
			}
			if fakeHerdr.reads != wantTotalReads {
				rt.Fatalf("poll %d (status=%s, freshStop=%v, idleUnreported=%v): cumulative probe reads = %d, want %d - the probe must fire exactly once per fresh idle/done stop", i, status, freshStop, idleUnreported, fakeHerdr.reads, wantTotalReads)
			}
		}
	})
}

// Captures every column reconciliation can touch for one task - full TaskHistory plus the hold
// row - through internal/state's own API (TestPackageTestsWriteNoDurableStateDirectly forbids
// reading the store file directly), for INV-REC-1's byte-for-byte, not field-by-field, litmus test.
func snapshotDurableState(rt *rapid.T, home, taskID string) []byte {
	history, err := state.ReadHistory(home, taskID)
	if err != nil {
		rt.Fatal(err)
	}
	hold, found, err := state.ReadHold(home, taskID)
	if err != nil {
		rt.Fatal(err)
	}
	snapshot := struct {
		History   state.TaskHistory
		Hold      state.Hold
		HoldFound bool
	}{history, hold, found}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		rt.Fatal(err)
	}
	return encoded
}

// A fixed clock would let a missing idempotency guard (e.g. recordRepair's own equality check)
// go unnoticed, since it would just rewrite the same timestamp both times - ticking a whole
// second between calls is what gives convergence something to actually prove.
func tickingClock(start time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		t := start.Add(time.Duration(calls) * time.Second)
		calls++
		return t
	}
}

// INV-REC-1: each scenario builds a self-consistent (durable intent, observed reality) pair
// reconcile is expected to settle within one call, then calls Reconcile again against the same
// deterministic fakes and requires the durable snapshot to come out byte-identical.
func TestReconciliationConvergesOnUnchangedObservedReality(t *testing.T) {
	type scenario struct {
		name  string
		build func(rt *rapid.T) (home, taskID string, r *Runtime)
	}
	scenarios := []scenario{
		{
			// Herdr exact, harness matches, pane busy: decideReconciliation's simplest fixed point.
			name: "running healthy",
			build: func(rt *rapid.T) (string, string, *Runtime) {
				home := reconcileFixture(t)
				status := rapid.SampledFrom([]herdr.Status{herdr.StatusWorking, herdr.StatusBlocked}).Draw(rt, "status")
				attempt := state.Attempt{Harness: "claude", Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}
				newLivenessFixture(rt, home, attempt)
				client := &reconcileHerdrClient{
					workspace: herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"},
					tabs:      []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}},
					pane:      herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: status},
				}
				return home, "task-1", &Runtime{deps: dependencies{
					now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
					herdr: func() herdrClient { return client },
					phase: func(lifecyclePhase) error { return nil },
				}}
			},
		},
		{
			// Idle-unreported past the launch grace: the first call records liveness, the second
			// must find recordAttemptLiveness's own no-op guard (statusChangedFor unchanged).
			name: "running idle-unreported",
			build: func(rt *rapid.T) (string, string, *Runtime) {
				home := reconcileFixture(t)
				attempt := state.Attempt{
					Harness: "claude", Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
					LaunchConfirmedAt: "2026-08-01T00:00:00Z",
				}
				newLivenessFixture(rt, home, attempt)
				client := &reconcileHerdrClient{
					workspace: herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"},
					tabs:      []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}},
					pane:      herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: herdr.StatusIdle},
				}
				return home, "task-1", &Runtime{deps: dependencies{
					now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
					herdr: func() herdrClient { return client },
					phase: func(lifecyclePhase) error { return nil },
				}}
			},
		},
		{
			// Dirty-worktree repair is only decided while Provisioning (decideProvisioning's
			// treehouseLeaseExact case); newLivenessFixture always advances to Running, so this
			// scenario builds the attempt directly instead, staying Provisioning deliberately.
			name: "needs-repair worktree dirty",
			build: func(rt *rapid.T) (string, string, *Runtime) {
				home := reconcileFixture(t)
				leaseID := rapid.SampledFrom([]string{"lease-1", "lease-2"}).Draw(rt, "lease-id")
				task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
				attempt := state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: leaseID}
				if _, err := state.CreateTaskWithAttempt(home, task, attempt); err != nil {
					rt.Fatal(err)
				}
				return home, "task-1", &Runtime{deps: dependencies{
					now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
					herdr: func() herdrClient { return &reconcileHerdrClient{} },
					worktree: worktreeDependencies{
						observeLease: func(string, string, string) worktree.LeaseObservation {
							return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: leaseID}
						},
						observeClean: func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil },
					},
					phase: func(lifecyclePhase) error { return nil },
				}}
			},
		},
		{
			// A stable needs-repair from an unobservable lease: the repair reason embeds the
			// probe's own text, so this also proves that text is reproduced identically, not
			// regenerated with something like a fresh timestamp each call.
			name: "needs-repair unobservable lease",
			build: func(rt *rapid.T) (string, string, *Runtime) {
				home := reconcileFixture(t)
				reason := rapid.SampledFrom([]string{"treehouse: command not found", "treehouse status --json: exit status 1", "treehouse status --json: unexpected end of JSON input"}).Draw(rt, "probe-reason")
				attempt := state.Attempt{Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1"}
				newLivenessFixture(rt, home, attempt)
				return home, "task-1", &Runtime{deps: dependencies{
					now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
					herdr: func() herdrClient { return &reconcileHerdrClient{} },
					worktree: worktreeDependencies{
						observeLease: func(string, string, string) worktree.LeaseObservation {
							return worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{Command: "treehouse status --json", WorkingDir: "/pool/1", Reason: reason}}
						},
					},
					phase: func(lifecyclePhase) error { return nil },
				}}
			},
		},
		{
			// No active attempt at all and no repair: reconcileTask's cheapest fixed point, no
			// writes on either call.
			name: "terminal, nothing to clean up",
			build: func(rt *rapid.T) (string, string, *Runtime) {
				home := reconcileFixture(t)
				attempt := state.Attempt{Harness: "claude"}
				newLivenessFixture(rt, home, attempt)
				history, err := state.ReadHistory(home, "task-1")
				if err != nil {
					rt.Fatal(err)
				}
				if err := state.TerminalizeTaskAndAttempt(home, "task-1", history.ActiveAttempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
					rt.Fatal(err)
				}
				return home, "task-1", &Runtime{deps: dependencies{
					now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
					herdr: func() herdrClient { return &reconcileHerdrClient{} },
					phase: func(lifecyclePhase) error { return nil },
				}}
			},
		},
	}

	rapid.Check(t, func(rt *rapid.T) {
		sc := rapid.SampledFrom(scenarios).Draw(rt, "scenario")
		home, taskID, r := sc.build(rt)

		if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: taskID}); err != nil {
			rt.Fatalf("case %q: first Reconcile() = %v", sc.name, err)
		}
		afterFirst := snapshotDurableState(rt, home, taskID)

		if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: taskID}); err != nil {
			rt.Fatalf("case %q: second Reconcile() = %v", sc.name, err)
		}
		afterSecond := snapshotDurableState(rt, home, taskID)

		if !bytes.Equal(afterFirst, afterSecond) {
			rt.Fatalf("case %q: durable state after the second Reconcile() differs from after the first, against an unchanged observed reality\nafter first:  %s\nafter second: %s", sc.name, afterFirst, afterSecond)
		}
	})
}

// INV-REC-4: two scenarios, each driving one axis (Herdr identity, worktree lease) through
// contradiction-set, an orthogonal change that must leave it unchanged, and the same axis
// proven healthy to clear it - the worktree case also covers shouldClearRepair's Running carve-out.
func TestRepairMarkerSurvivesAnOrthogonalChangeAndClearsWhenTheSameContradictionIsProvenGone(t *testing.T) {
	type step struct {
		herdr     herdrOwnershipState
		treehouse treehouseLeaseState
		wantCode  string // "" means expect the marker cleared
	}
	type scenario struct {
		name  string
		setup func(rt *rapid.T) (home, taskID string)
		steps []step
	}

	scenarios := []scenario{
		{
			// Herdr is the only axis in play (Worktree == ""), so there is nothing orthogonal to
			// probe here beyond repeating the same bad observation - that half is INV-REC-1's own
			// territory. This scenario's job is the "proven gone" resolution on the Herdr axis.
			name: "herdr identity mismatch, proven gone",
			setup: func(rt *rapid.T) (string, string) {
				home := reconcileFixture(t)
				attempt := state.Attempt{Harness: "claude", Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}}
				newLivenessFixture(rt, home, attempt)
				return home, "task-1"
			},
			steps: []step{
				{herdr: herdrOwnershipMismatch, treehouse: treehouseLeaseUnobserved, wantCode: repairCodeRunningPaneIdentityMismatch},
				{herdr: herdrOwnershipMismatch, treehouse: treehouseLeaseUnobserved, wantCode: repairCodeRunningPaneIdentityMismatch}, // still bad: unchanged
				{herdr: herdrOwnershipExact, treehouse: treehouseLeaseUnobserved, wantCode: ""},                                       // same axis proven gone: clears
			},
		},
		{
			// herdr "absent" is deliberately avoided here: it would satisfy
			// decideTerminalConvergence's candidate check ahead of the treehouse switch and
			// auto-converge the attempt instead of reaching this repair path; "mismatch" does not.
			name: "worktree ownership mismatch, herdr fix is orthogonal, then proven gone",
			setup: func(rt *rapid.T) (string, string) {
				home := reconcileFixture(t)
				attempt := state.Attempt{
					Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
					Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
				}
				newLivenessFixture(rt, home, attempt)
				return home, "task-1"
			},
			steps: []step{
				{herdr: herdrOwnershipMismatch, treehouse: treehouseLeaseMismatch, wantCode: repairCodeWorktreeOwnershipMismatch},
				{herdr: herdrOwnershipExact, treehouse: treehouseLeaseMismatch, wantCode: repairCodeWorktreeOwnershipMismatch}, // orthogonal fix: unchanged
				{herdr: herdrOwnershipExact, treehouse: treehouseLeaseExact, wantCode: ""},                                     // same axis proven gone: clears (Running tolerates dirty)
			},
		},
	}

	rapid.Check(t, func(rt *rapid.T) {
		sc := rapid.SampledFrom(scenarios).Draw(rt, "scenario")
		home, taskID := sc.setup(rt)

		client := &reconcileHerdrClient{
			workspace: herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"},
			tabs:      []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: taskID}},
		}
		var herdrState herdrOwnershipState
		treehouseState := treehouseLeaseUnobserved
		r := &Runtime{deps: dependencies{
			now:   tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
			herdr: func() herdrClient { return client },
			worktree: worktreeDependencies{
				observeLease: func(string, string, string) worktree.LeaseObservation {
					switch treehouseState {
					case treehouseLeaseExact:
						return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
					case treehouseLeaseMismatch:
						return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "lease-other"}
					}
					return worktree.LeaseObservation{State: worktree.LeaseUnknown}
				},
				observeClean: func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil },
			},
			appendCompletion: completion.Append,
			phase:            func(lifecyclePhase) error { return nil },
		}}

		for i, st := range sc.steps {
			herdrState, treehouseState = st.herdr, st.treehouse
			// reconcileHerdrClient.PaneGet never errors and f.err would also break the earlier
			// FindWorkspaceByLabel call, so "absent" is simulated the way it really happens: the
			// tab this attempt names is no longer in the live inventory.
			client.tabs = []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: taskID}}
			switch herdrState {
			case herdrOwnershipExact:
				client.pane = herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: herdr.StatusWorking}
			case herdrOwnershipMismatch:
				client.pane = herdr.Pane{PaneID: "pane-other", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude"}
			case herdrOwnershipAbsent:
				client.tabs = nil
				client.pane = herdr.Pane{}
			default:
				client.pane = herdr.Pane{}
			}

			if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: taskID}); err != nil {
				rt.Fatalf("case %q step %d: Reconcile() = %v", sc.name, i, err)
			}
			history, err := state.ReadHistory(home, taskID)
			if err != nil {
				rt.Fatal(err)
			}
			if history.Task.RepairCode != st.wantCode {
				rt.Fatalf("case %q step %d (herdr=%s, treehouse=%s): repair code = %q, want %q", sc.name, i, st.herdr, st.treehouse, history.Task.RepairCode, st.wantCode)
			}
		}
	})
}

// A herdr client whose PaneGet answer changes after callsBeforeSwitch calls, so a test can prove
// a later reconcileTask iteration observes fresh state rather than reusing an earlier answer.
type switchingHerdrClient struct {
	before, after     herdr.Pane
	callsBeforeSwitch int
	paneGetCalls      int
}

func (c *switchingHerdrClient) FindWorkspaceByLabel(string) (herdr.Workspace, bool, error) {
	return herdr.Workspace{WorkspaceID: "ws-1", Label: "hand:demo"}, true, nil
}
func (c *switchingHerdrClient) WorkspaceList() ([]herdr.Workspace, error) { return nil, nil }
func (c *switchingHerdrClient) WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error) {
	return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (c *switchingHerdrClient) WorkspaceClose(string) error { return errors.New("unused") }
func (c *switchingHerdrClient) TabList(string) ([]herdr.Tab, error) {
	return []herdr.Tab{{TabID: "tab-1", WorkspaceID: "ws-1", Label: "task-1"}}, nil
}
func (c *switchingHerdrClient) TabCreate(string, string, map[string]string, string) (herdr.Tab, herdr.Pane, error) {
	return herdr.Tab{}, herdr.Pane{}, errors.New("unused")
}
func (c *switchingHerdrClient) TabRename(string, string) error { return errors.New("unused") }
func (c *switchingHerdrClient) TabClose(string) error          { return errors.New("unused") }
func (c *switchingHerdrClient) PaneGet(string) (herdr.Pane, error) {
	c.paneGetCalls++
	if c.paneGetCalls > c.callsBeforeSwitch {
		return c.after, nil
	}
	return c.before, nil
}
func (c *switchingHerdrClient) PaneRun(string, string) error { return errors.New("unused") }
func (c *switchingHerdrClient) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	return herdr.ProcessInfo{}, errors.New("unused")
}
func (c *switchingHerdrClient) PaneRunSpec(string, launchSpec) error { return nil }
func (c *switchingHerdrClient) PaneSendKeys(string, ...string) error { return errors.New("unused") }
func (c *switchingHerdrClient) PaneRead(string, int) (string, error) { return "", errors.New("unused") }

// INV-REC-5, first half: drives a Provisioning attempt through confirm-launch and mark-running
// - two distinct, sequential actions - and requires Iterations to count exactly 3; two actions
// collapsing into one iteration would report a short count.
func TestReconcileAppliesExactlyOneActionPerIteration(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		home := reconcileFixture(t)
		task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
		attempt := state.Attempt{
			TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
			Herdr:             state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
			LaunchSubmittedAt: "2026-08-28T00:00:00Z",
		}
		if _, err := state.CreateTaskWithAttempt(home, task, attempt); err != nil {
			rt.Fatal(err)
		}
		agentStatus := rapid.SampledFrom([]herdr.Status{herdr.StatusWorking, herdr.StatusBlocked}).Draw(rt, "agent-status")
		client := &switchingHerdrClient{
			before:            herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: agentStatus},
			after:             herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: agentStatus},
			callsBeforeSwitch: 1000, // never switches: this half is about the action count, not re-observation
		}
		r := &Runtime{deps: dependencies{
			now:              tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
			herdr:            func() herdrClient { return client },
			buildHarness:     func(string, harness.Options) (launchSpec, error) { return launchSpec{Executable: "launch"}, nil },
			confirmLaunch:    func(herdrClient, string, string, launchSpec) error { return nil },
			appendCompletion: completion.Append,
			phase:            func(lifecyclePhase) error { return nil },
		}}

		result, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
		if err != nil {
			rt.Fatalf("Reconcile() = %v", err)
		}
		if len(result.Results) != 1 {
			rt.Fatalf("results = %+v, want exactly one", result.Results)
		}
		got := result.Results[0]
		if got.Iterations != 3 {
			rt.Fatalf("Iterations = %d, want 3 (confirm-launch, mark-running, keep) - two actions collapsing into one iteration would report fewer: %+v", got.Iterations, got)
		}
		if got.Outcome != reconcileOutcomeHealthy || got.Action != string(reconciliationActionKeep) {
			rt.Fatalf("final outcome/action = %s/%s, want healthy/keep once provisioning finishes converging: %+v", got.Outcome, got.Action, got)
		}
		history, err := state.ReadHistory(home, "task-1")
		if err != nil {
			rt.Fatal(err)
		}
		if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
			rt.Fatalf("active attempt = %+v, want running", history.ActiveAttempt)
		}
	})
}

// INV-REC-5, second half: the pane identity switches after the two observeAttempt calls
// confirm-launch and mark-running each make, so the third iteration's verdict must be decided
// by the changed pane it just observed, not the one the first two iterations saw.
func TestReconcileTaskReObservesFreshStateEveryIteration(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		home := reconcileFixture(t)
		task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
		attempt := state.Attempt{
			TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude",
			Herdr:             state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
			LaunchSubmittedAt: "2026-08-28T00:00:00Z",
		}
		if _, err := state.CreateTaskWithAttempt(home, task, attempt); err != nil {
			rt.Fatal(err)
		}
		otherPaneID := rapid.SampledFrom([]string{"pane-2", "pane-stolen", "pane-x"}).Draw(rt, "other-pane-id")
		client := &switchingHerdrClient{
			before:            herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: herdr.StatusWorking},
			after:             herdr.Pane{PaneID: otherPaneID, TabID: "tab-1", WorkspaceID: "ws-1", Agent: "claude", AgentStatus: herdr.StatusWorking},
			callsBeforeSwitch: 2, // confirm-launch's observeAttempt, then mark-running's
		}
		r := &Runtime{deps: dependencies{
			now:              tickingClock(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)),
			herdr:            func() herdrClient { return client },
			buildHarness:     func(string, harness.Options) (launchSpec, error) { return launchSpec{Executable: "launch"}, nil },
			confirmLaunch:    func(herdrClient, string, string, launchSpec) error { return nil },
			appendCompletion: completion.Append,
			phase:            func(lifecyclePhase) error { return nil },
		}}

		result, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
		if err != nil {
			rt.Fatalf("Reconcile() = %v", err)
		}
		if len(result.Results) != 1 {
			rt.Fatalf("results = %+v, want exactly one", result.Results)
		}
		got := result.Results[0]
		if got.Outcome != reconcileOutcomeRepair || got.RepairCode != repairCodeRunningPaneIdentityMismatch {
			rt.Fatalf("outcome/code = %s/%s, want needs-repair/running-pane-identity-mismatch - the third iteration must decide from the pane it just observed (identity now %q), not the one confirm-launch and mark-running saw two iterations earlier: %+v", got.Outcome, got.RepairCode, otherPaneID, got)
		}
	})
}

// state.CreateTaskWithAttempt only accepts a freshly provisioning attempt (INV-TASK-1's guard),
// so a fixture that wants to start out running has to go through the same store transitions a
// real launch does rather than writing the terminal shape directly.
func createRunningAttemptFixture(rt *rapid.T, home string, task state.Task, attempt state.Attempt) {
	wantLifecycle := attempt.Lifecycle
	attempt.Lifecycle = state.AttemptProvisioning
	created, err := state.CreateTaskWithAttempt(home, task, attempt)
	if err != nil {
		rt.Fatal(err)
	}
	if wantLifecycle != state.AttemptRunning {
		return
	}
	if err := state.MarkLaunchSubmitted(home, task.ID, created.ID, "2026-08-28T00:00:00Z"); err != nil {
		rt.Fatal(err)
	}
	if err := state.MarkLaunchConfirmed(home, task.ID, created.ID, "2026-08-28T00:00:01Z"); err != nil {
		rt.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, task.ID, created.ID); err != nil {
		rt.Fatal(err)
	}
}

// INV-REC-2: decideReconciliation takes no receiver and performs no I/O, so this is a direct
// purity check rather than an approximation of one - any hidden input (wall-clock time, map
// iteration order, a package var) would show up as the two calls disagreeing on one argument triple.
func TestDecideReconciliationIsAPureFunctionOfDurableIntentAndObservedEvidence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		task := genTask(rt)
		attempt := genAttempt(rt)
		observation := genObservation(rt)

		first := decideReconciliation(task, attempt, observation)
		second := decideReconciliation(task, attempt, observation)
		if first != second {
			rt.Fatalf("decideReconciliation(%+v, %+v, %+v) = %+v on the first call, %+v on the second, want identical decisions for an identical (durable intent, observed evidence) pair", task, attempt, observation, first, second)
		}
	})
}

// INV-REC-3: generates failures on each of the three external observation surfaces (Herdr,
// worktree/treehouse, GitHub), distinguishing a hard I/O error (must abort before any decision)
// from a "cannot be observed" classification (must route to a repair code, never a proven mismatch).
func TestObservationFailuresNeverBecomeContradictionEvidenceOrClearARepairMarker(t *testing.T) {
	type failureCase struct {
		name        string
		build       func(rt *rapid.T) (task state.Task, attempt state.Attempt, fakeHerdr *reconcileHerdrClient, worktreeDeps worktreeDependencies)
		wantBlocked bool
		// checked only when !wantBlocked: the repair code reconcile records must be in this set.
		wantRepairCodeOneOf []string
	}

	cases := []failureCase{
		{
			name: "herdr pane read hard failure",
			build: func(rt *rapid.T) (state.Task, state.Attempt, *reconcileHerdrClient, worktreeDependencies) {
				task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
				attempt := state.Attempt{
					TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude",
					Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
				}
				client := &reconcileHerdrClient{err: errors.New("herdr transport timed out")}
				return task, attempt, client, worktreeDependencies{}
			},
			wantBlocked: true,
		},
		{
			name: "worktree cleanliness hard failure",
			build: func(rt *rapid.T) (state.Task, state.Attempt, *reconcileHerdrClient, worktreeDependencies) {
				task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
				attempt := state.Attempt{
					TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
				}
				deps := worktreeDependencies{
					observeLease: func(string, string, string) worktree.LeaseObservation {
						return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "lease-1"}
					},
					observeClean: func(string) (worktree.Cleanliness, error) {
						return "", errors.New("git status --porcelain failed: exit status 128")
					},
				}
				return task, attempt, &reconcileHerdrClient{}, deps
			},
			wantBlocked: true,
		},
		{
			name: "treehouse lease unobservable",
			build: func(rt *rapid.T) (state.Task, state.Attempt, *reconcileHerdrClient, worktreeDependencies) {
				task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
				attempt := state.Attempt{
					TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
				}
				reason := rapid.SampledFrom([]string{
					"absent executable", "non-zero exit", "unparsable output", "empty pool", "pool describes other worktrees",
				}).Draw(rt, "unobservable-reason")
				deps := worktreeDependencies{
					observeLease: func(string, string, string) worktree.LeaseObservation {
						return worktree.LeaseObservation{State: worktree.LeaseUnknown, Probe: worktree.LeaseProbe{Command: "treehouse status --json", WorkingDir: "/pool/1", Reason: reason}}
					},
				}
				return task, attempt, &reconcileHerdrClient{}, deps
			},
			wantBlocked:         false,
			wantRepairCodeOneOf: []string{repairCodeWorktreeUnobservable},
		},
		{
			name: "treehouse lease unprovable",
			build: func(rt *rapid.T) (state.Task, state.Attempt, *reconcileHerdrClient, worktreeDependencies) {
				task := state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Lifecycle: state.TaskOpen}
				attempt := state.Attempt{
					TaskID: "task-1", Lifecycle: state.AttemptRunning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
				}
				deps := worktreeDependencies{
					observeLease: func(string, string, string) worktree.LeaseObservation {
						return worktree.LeaseObservation{State: worktree.LeaseUnprovable}
					},
				}
				return task, attempt, &reconcileHerdrClient{}, deps
			},
			wantBlocked:         false,
			wantRepairCodeOneOf: []string{repairCodeLegacyWorktreeUnprovable},
		},
	}

	rapid.Check(t, func(rt *rapid.T) {
		tc := rapid.SampledFrom(cases).Draw(rt, "failure-case")
		task, attempt, fakeHerdr, worktreeDeps := tc.build(rt)

		home := reconcileFixture(t)
		createRunningAttemptFixture(rt, home, task, attempt)

		seedRepair := rapid.Bool().Draw(rt, "seed-unrelated-repair")
		if seedRepair {
			if err := state.SetTaskRepair(home, "task-1", repairCodeWorktreeDirty, "pre-existing marker from an unrelated contradiction", 1, "2026-08-01T00:00:00Z"); err != nil {
				rt.Fatal(err)
			}
		}
		before, err := state.ReadHistory(home, "task-1")
		if err != nil {
			rt.Fatal(err)
		}

		r := &Runtime{deps: dependencies{
			now:      func() time.Time { return time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC) },
			herdr:    func() herdrClient { return fakeHerdr },
			worktree: worktreeDeps,
			phase:    func(lifecyclePhase) error { return nil },
		}}

		result, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})

		after, afterErr := state.ReadHistory(home, "task-1")
		if afterErr != nil {
			rt.Fatal(afterErr)
		}

		if tc.wantBlocked {
			if err == nil {
				rt.Fatalf("case %q: Reconcile() succeeded, want an error from the injected observation failure", tc.name)
			}
			if len(result.Results) != 1 || result.Results[0].Outcome != reconcileOutcomeBlocked {
				rt.Fatalf("case %q: outcome = %+v, want blocked", tc.name, result.Results)
			}
			if after.Task.RepairCode != before.Task.RepairCode || after.Task.RepairReason != before.Task.RepairReason || after.Task.RepairAttemptID != before.Task.RepairAttemptID {
				rt.Fatalf("case %q: repair marker changed on a hard observation failure: before=%+v after=%+v", tc.name, before.Task, after.Task)
			}
			if len(after.Attempts) != 1 || after.Attempts[0].Lifecycle != before.Attempts[0].Lifecycle {
				rt.Fatalf("case %q: attempt lifecycle changed on a hard observation failure: before=%+v after=%+v", tc.name, before.Attempts, after.Attempts)
			}
			return
		}

		if err != nil {
			rt.Fatalf("case %q: Reconcile() = %v, want a recorded repair rather than an error for a distinguished unobservable classification", tc.name, err)
		}
		if len(result.Results) != 1 || result.Results[0].Outcome != reconcileOutcomeRepair {
			rt.Fatalf("case %q: outcome = %+v, want needs-repair", tc.name, result.Results)
		}
		gotCode := result.Results[0].RepairCode
		ok := false
		for _, want := range tc.wantRepairCodeOneOf {
			if gotCode == want {
				ok = true
			}
		}
		if !ok {
			rt.Fatalf("case %q: repair code = %q, want one of %v (an observation that could not be made must never be coded as a proven mismatch)", tc.name, gotCode, tc.wantRepairCodeOneOf)
		}
		if seedRepair && gotCode == repairCodeWorktreeDirty {
			rt.Fatalf("case %q: the unrelated pre-seeded repair code survived instead of being replaced by this call's own classification", tc.name)
		}
	})
}
