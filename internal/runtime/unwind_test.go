package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

// A pane whose text and agent come from a file and an environment variable the test controls, so one
// installed herdr fake serves both a launch that refuses on a first-run dialog and the later launch
// that confirms.
func fakeSwitchablePane(t *testing.T, agent string) func(string) {
	t.Helper()
	dir := t.TempDir()
	textFile := filepath.Join(dir, "pane.txt")
	bin := faketool.Bin(t)
	faketool.Herdr{
		Creates: []faketool.HerdrWorkspace{
			{ID: "ws-1", Label: "hand:demo", Tabs: []faketool.HerdrTab{{ID: "tab-1", Label: "1", Pane: "pane-1"}}},
			{ID: "ws-2", Label: "hand:demo", Tabs: []faketool.HerdrTab{{ID: "tab-2", Label: "1", Pane: "pane-2"}}},
		},
		PaneAgent: agent, PaneReadFileEnv: true, PaneStatus: "idle",
		KeyLog: filepath.Join(dir, "keys.log"),
	}.Install(t, bin)
	t.Setenv("PANE_AGENT", agent)
	t.Setenv("PANE_TEXT_FILE", textFile)
	return func(text string) {
		if err := os.WriteFile(textFile, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The reconcile runtime with the two dependencies this path has to exercise for real: the Herdr client
// that talks to the installed fake, and the launch confirmation that reads the pane.
func unwindRuntime(t *testing.T, returns *int) (*Runtime, string) {
	t.Helper()
	pool := t.TempDir()
	worktreePath := filepath.Join(pool, "slot-1")
	r := reconcileRuntime(nil, func(string, string) (worktree.Lease, error) {
		return worktree.Lease{Path: worktreePath, ID: "lease-1"}, nil
	})
	r.deps.herdr = func() herdrClient { return herdr.NewClient() }
	r.deps.buildHarness = harness.Build
	r.deps.confirmLaunch = confirmLaunch
	r.deps.worktree.returnWorktree = func(string, string, bool) error { *returns++; return nil }
	r.deps.worktree.returnWithID = func(string, string, string, bool) error { *returns++; return nil }
	return r, worktreePath
}

// The whole path atqamz/hand#254 exists for, driven only through supported commands: a spawn refused
// at a first-run dialog hand will not answer, the reconcile that unwinds what it left, and the reopen
// that spawns the task again.
func TestSpawnRefusedAtTheTrustPromptUnwindsAndSpawnsAgain(t *testing.T) {
	useFastLaunchPolling(t)
	home := executionPlanHome(t, "brief\n")
	addHarnessToPath(t, harness.Codex)
	paneText := fakeSwitchablePane(t, harness.Codex)
	paneText(launchCodexTrustFrame)
	returns := 0
	r, worktreePath := unwindRuntime(t, &returns)

	_, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Harness: harness.Codex, HarnessFromFlag: true,
	})
	if err == nil || !strings.Contains(err.Error(), "waiting on the directory trust prompt") || !strings.Contains(err.Error(), worktreePath) {
		t.Fatalf("Spawn() = %v, want the launch refused at the codex directory trust prompt", err)
	}
	failed := readOnlyAttempt(t, home)
	if failed.Lifecycle != state.AttemptProvisioning || failed.LaunchSubmittedAt == "" || failed.LaunchConfirmedAt != "" ||
		failed.Worktree != "" || failed.LeaseID != "" || hasHerdrIdentity(failed.Herdr) {
		t.Fatalf("attempt after the refused launch = %+v, want launch evidence with every acquired resource released", failed)
	}
	if returns != 1 {
		t.Fatalf("worktree returns after the refused launch = %d, want the acquired lease returned once", returns)
	}

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() = %v, want the failed provisioning unwound", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	unwound := history.Attempts[0]
	if history.Task.Lifecycle != state.TaskTerminal || unwound.Lifecycle != state.AttemptInterrupted ||
		unwound.TeardownDisposition != state.TeardownDispositionProvisioningUnwound {
		t.Fatalf("task=%+v attempt=%+v, want a terminal task and an interrupted attempt disposed as provisioning-unwound", history.Task, unwound)
	}
	if unwound.Worktree != "" || unwound.LeaseID != "" || hasHerdrIdentity(unwound.Herdr) ||
		unwound.TeardownWorktreeState != "" || unwound.TeardownHerdrState != "" {
		t.Fatalf("unwound attempt = %+v, want no ownership claim and no resource state invented by the unwind", unwound)
	}
	if returns != 1 {
		t.Fatalf("worktree returns after the unwind = %d, want the unwind to release nothing of its own", returns)
	}
	if unwound.LaunchSubmittedAt != failed.LaunchSubmittedAt || unwound.CreatedAt != failed.CreatedAt || unwound.Ordinal != 1 {
		t.Fatalf("unwound attempt = %+v, want the refused launch still readable as attempt 1", unwound)
	}
	record, found, err := completion.FindAttempt(home, unwound.ID)
	if err != nil || !found || record.AttemptLifecycle != string(state.AttemptInterrupted) {
		t.Fatalf("completion record = %+v found=%t err=%v, want the unwound attempt accounted for", record, found, err)
	}

	paneText("codex ready\n")
	reopenReq := ReopenRequest{Home: home, ID: "task-1", Harness: harness.Codex, HarnessFromFlag: true}
	if _, err := r.Reopen(context.Background(), reopenReq); err != nil {
		t.Fatalf("Reopen() = %v, want the unwound task to spawn again through the supported path", err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 || history.Attempts[0].ID != unwound.ID || history.Attempts[0].Lifecycle != state.AttemptInterrupted {
		t.Fatalf("history after reopen = %+v, want the unwound attempt kept beside the new one", history.Attempts)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning ||
		history.ActiveAttempt.Herdr.PaneID == "" || history.ActiveAttempt.Worktree == "" {
		t.Fatalf("active attempt after reopen = %+v, want a running attempt with its own pane and worktree", history.ActiveAttempt)
	}
}

func readOnlyAttempt(t *testing.T, home string) state.Attempt {
	t.Helper()
	history, err := state.ReadHistoryReadOnly(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt == nil {
		t.Fatal("task-1 has no active attempt")
	}
	return *history.ActiveAttempt
}

// The converse of the unwind: every fact that makes unwinding provably safe is a fact reconcile has
// to check, so a failed launch that did persist a resource is diagnosed instead of unwound.
func TestReconcileRefusesToUnwindProvisioningThatOwnsAResource(t *testing.T) {
	for _, test := range []struct {
		name     string
		attempt  state.Attempt
		wantCode string
	}{
		{
			name:     "a persisted pane identity",
			attempt:  state.Attempt{Herdr: state.Herdr{Session: "default", WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}},
			wantCode: repairCodeLaunchSubmittedPaneMissing,
		},
		{
			name:     "a partial pane identity",
			attempt:  state.Attempt{Herdr: state.Herdr{WorkspaceID: "ws-1"}},
			wantCode: repairCodeHerdrOwnershipIncomplete,
		},
		{
			name:     "a recorded worktree lease",
			attempt:  state.Attempt{Worktree: "/pool/1", LeaseID: "lease-1"},
			wantCode: repairCodeProvisioningPaneMissing,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := reconcileFixture(t)
			attempt := test.attempt
			attempt.TaskID, attempt.Lifecycle, attempt.Harness = "task-1", state.AttemptProvisioning, "claude"
			attempt.LaunchSubmittedAt = "2026-08-15T00:00:00Z"
			if _, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Brief: "data/task-1/brief.md"}, attempt); err != nil {
				t.Fatal(err)
			}
			r := reconcileRuntime(&missingReconcileHerdr{}, nil)
			if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
				t.Fatal(err)
			}
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Task.RepairCode != test.wantCode || history.Task.Lifecycle != state.TaskOpen ||
				history.Attempts[0].Lifecycle != state.AttemptProvisioning {
				t.Fatalf("task=%+v attempt=%+v, want %s recorded without unwinding the attempt", history.Task, history.Attempts[0], test.wantCode)
			}
			if err := r.unwindFailedProvisioning(home, history.Task, history.Attempts[0], reconciliationDecision{
				TerminalAttempt: state.AttemptInterrupted, Disposition: state.TeardownDispositionProvisioningUnwound,
			}); err == nil || !strings.Contains(err.Error(), "not a launch that left nothing behind") {
				t.Fatalf("unwindFailedProvisioning() = %v, want a refusal naming what the attempt still records", err)
			}
		})
	}
}

// The pane attestation is for ownership no observation settles. Where an observation does settle it,
// reconcile either cleans the pane up or has nothing to relinquish, so the attestation refuses.
func TestAbandonHistoricalPaneRefusesOwnershipAnObservationSettles(t *testing.T) {
	for _, observed := range []herdrOwnershipState{herdrOwnershipExact, herdrOwnershipAbsent, herdrOwnershipUnobserved} {
		t.Run(string(observed), func(t *testing.T) {
			home := reconcileFixture(t)
			attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
				TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
				t.Fatal(err)
			}
			attempt.Lifecycle = state.AttemptInterrupted
			r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
			_, _, err = r.abandonHistoricalPane(home, state.Task{ID: "task-1"}, attempt, herdrObservation{State: observed})
			if err == nil || !strings.Contains(err.Error(), "abandonment is only for ownership neither observation can settle") {
				t.Fatalf("abandonHistoricalPane(%s) = %v, want a refusal", observed, err)
			}
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Attempts[0].TeardownHerdrState != "" {
				t.Fatalf("herdr resource state = %q, want a refused attestation to record nothing", history.Attempts[0].TeardownHerdrState)
			}
		})
	}
}

// No automatic path releases a resource whose ownership cannot be proven: without the attestation the
// unprovable pane stays claimed, diagnosed, and closed by nobody.
func TestReconcileLeavesAnUnprovablePaneClaimedWithoutAnAttestation(t *testing.T) {
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptProvisioning, state.AttemptInterrupted); err != nil {
		t.Fatal(err)
	}
	client := &healthyReconcileHerdr{}
	r := reconcileRuntime(client, nil)
	for range 2 {
		if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeTeardownResourceAmbiguous || history.Attempts[0].TeardownHerdrState == state.TeardownResourceAbandoned ||
		history.Attempts[0].TeardownHerdrState == state.TeardownResourceReleased || client.closed != 0 {
		t.Fatalf("task=%+v attempt=%+v closed=%d, want the unprovable pane still claimed and untouched", history.Task, history.Attempts[0], client.closed)
	}
}
