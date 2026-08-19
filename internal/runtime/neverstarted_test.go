package runtime

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

// The stuck state that emits no diagnosis at all: a running Attempt whose worker took no turn looks
// exactly like one that is working, so only the operator can end it, and the exit has to run through
// the ordinary release path rather than around it (atqamz/hand#254).
func TestAttemptNeverStartedAttestationEndsTheAttemptHonestly(t *testing.T) {
	useFastLaunchPolling(t)
	home := executionPlanHome(t, "brief\n")
	addHarnessToPath(t, harness.Codex)
	paneText := fakeSwitchablePane(t, harness.Codex)
	paneText("codex ready\n")
	returns := 0
	r := unwindRuntime(t, &returns)

	if _, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Harness: harness.Codex, HarnessFromFlag: true,
	}); err != nil {
		t.Fatalf("Spawn() = %v, want a running attempt", err)
	}
	launched := readOnlyAttempt(t, home)
	if launched.Lifecycle != state.AttemptRunning || launched.Worktree == "" || !hasHerdrIdentity(launched.Herdr) {
		t.Fatalf("attempt after spawn = %+v, want a running attempt holding both resources", launched)
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() = %v, want the running attempt left alone", err)
	}
	if readOnlyAttempt(t, home).Lifecycle != state.AttemptRunning {
		t.Fatal("plain reconcile ended the attempt, want a worker Hand cannot observe left running")
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindLimit, Reason: "usage limit", SetAt: "2026-08-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AttemptNeverStarted: true})
	if err != nil {
		t.Fatalf("Reconcile(AttemptNeverStarted) = %v, want the attested attempt ended", err)
	}
	if len(report.Results) != 1 || !strings.Contains(report.Results[0].Detail, "operator attestation that its worker never started") {
		t.Fatalf("reconcile report = %+v, want the attestation named in what it reports", report.Results)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	ended := history.Attempts[0]
	if history.Task.Lifecycle != state.TaskTerminal || ended.Lifecycle != state.AttemptInterrupted ||
		ended.TeardownDisposition != state.TeardownDispositionWorkerNeverStarted {
		t.Fatalf("task=%+v attempt=%+v, want a terminal task and an interrupted attempt disposed as worker-never-started", history.Task, ended)
	}
	if ended.TeardownHerdrState != state.TeardownResourceReleased || ended.TeardownWorktreeState != state.TeardownResourceReleased {
		t.Fatalf("resource states = herdr %q worktree %q, want both released by the ordinary path", ended.TeardownHerdrState, ended.TeardownWorktreeState)
	}
	if returns != 1 {
		t.Fatalf("worktree returns = %d, want the lease returned exactly once", returns)
	}
	record, found, err := completion.FindAttempt(home, ended.ID)
	if err != nil || !found {
		t.Fatalf("completion record found=%t err=%v, want the ended attempt accounted for", found, err)
	}
	if record.Outcome != "torn-down" || !strings.Contains(record.Detail, "worker never started") {
		t.Fatalf("completion record = %+v, want an outcome that claims nothing about work", record)
	}
	if _, hasHold, err := state.ReadHold(home, "task-1"); err != nil {
		t.Fatal(err)
	} else if hasHold {
		t.Fatal("usage-limit hold survived the attestation, want reconcile terminalization to clear it so reopen stays reachable")
	}

	if _, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1", Harness: harness.Codex, HarnessFromFlag: true}); err != nil {
		t.Fatalf("Reopen() = %v, want the ended task to spawn again through the supported path", err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 || history.Attempts[0].ID != ended.ID ||
		history.Attempts[0].TeardownDisposition != state.TeardownDispositionWorkerNeverStarted {
		t.Fatalf("history after reopen = %+v, want the ended attempt kept as it was beside the new one", history.Attempts)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("active attempt after reopen = %+v, want a running attempt", history.ActiveAttempt)
	}
}

// The attestation is about one fact and refuses whenever durable state or an observation disproves it,
// because an operator asserting that a worker took no turn cannot overrule what a turn left behind.
func TestAttemptNeverStartedAttestationRefusesEvidenceOfWork(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(t *testing.T, home string, r *Runtime, attempt state.Attempt)
		wantErr string
	}{
		{
			name: "reported-line",
			arrange: func(t *testing.T, home string, _ *Runtime, _ state.Attempt) {
				if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("working: first turn\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "reported line",
		},
		{
			name: "recorded-pull-request",
			arrange: func(t *testing.T, home string, _ *Runtime, _ state.Attempt) {
				if err := state.SetTaskPR(home, "task-1", "https://github.com/demo/demo/pull/1"); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "pull request https://github.com/demo/demo/pull/1 is recorded",
		},
		{
			name: "dirty-worktree",
			arrange: func(_ *testing.T, _ string, r *Runtime, _ state.Attempt) {
				r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
			},
			wantErr: "holds uncommitted changes",
		},
		{
			name: "local-only-commits",
			arrange: func(_ *testing.T, _ string, r *Runtime, _ state.Attempt) {
				repairCommitSafety(r, worktree.CommitSafetyLocalOnly, worktree.CommitSafetyProbe{LocalOnly: 2})
			},
			wantErr: "reachable from no remote-tracking ref",
		},
		{
			name: "decision-already-recorded",
			arrange: func(t *testing.T, home string, _ *Runtime, attempt state.Attempt) {
				repairTeardownDecision(t, home, attempt, state.AttemptCompleted, state.TeardownDispositionCompleted)
			},
			wantErr: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, attempt := repairFixture(t, state.Task{},
				repairRunningAttempt("/pool/1", "lease-1", state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}))
			repairMarkRunning(t, home, attempt)
			r := repairRuntime(&repairHerdr{})
			test.arrange(t, home, r, attempt)

			_, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1", AttemptNeverStarted: true})
			after, readErr := state.ReadHistory(home, "task-1")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Reconcile(AttemptNeverStarted) = %v, want the recorded decision honoured instead", err)
				}
				if after.Attempts[0].TeardownDisposition != state.TeardownDispositionCompleted {
					t.Fatalf("disposition = %q, want the recorded decision left alone", after.Attempts[0].TeardownDisposition)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Reconcile(AttemptNeverStarted) = %v, want a refusal naming %q", err, test.wantErr)
			}
			if !strings.Contains(err.Error(), "hand teardown task-1") {
				t.Fatalf("refusal = %v, want it to name the supported command that ends the attempt instead", err)
			}
			if after.Attempts[0].TeardownDisposition != "" || after.Attempts[0].Lifecycle != state.AttemptRunning ||
				after.Task.Lifecycle != state.TaskOpen {
				t.Fatalf("state after the refusal = task %+v attempt %+v, want nothing recorded", after.Task, after.Attempts[0])
			}
		})
	}
}

// A lifecycle other than running is refused whatever the operator asserts, because every other
// lifecycle already has evidence of its own that reconcile acts on.
func TestAttemptNeverStartedAttestationRefusesAnAttemptThatIsNotRunning(t *testing.T) {
	home, attempt := repairFixture(t, state.Task{}, state.Attempt{
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z",
	})
	r := repairRuntime(&repairHerdr{})
	_, err := r.attestAttemptNeverStarted(home, state.Task{ID: "task-1"}, attempt)
	if err == nil || !strings.Contains(err.Error(), "records lifecycle \"provisioning\"") {
		t.Fatalf("attestAttemptNeverStarted() = %v, want a refusal naming the lifecycle", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].TeardownDisposition != "" {
		t.Fatalf("disposition = %q, want a refused attestation to record nothing", history.Attempts[0].TeardownDisposition)
	}
}
