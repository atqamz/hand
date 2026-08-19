package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

const commitSafetyHead = "1111111111111111111111111111111111111111"

func remoteObservedCommits(head string) worktree.CommitSafetyObservation {
	return worktree.CommitSafetyObservation{
		State: worktree.CommitSafetyRemoteObserved,
		Probe: worktree.CommitSafetyProbe{Command: "git rev-list --count HEAD --not --remotes", WorkingDir: "/pool/1", Head: head, RemoteRefs: 2},
	}
}

func localOnlyCommits(head string, count int) worktree.CommitSafetyObservation {
	return worktree.CommitSafetyObservation{
		State: worktree.CommitSafetyLocalOnly,
		Probe: worktree.CommitSafetyProbe{Command: "git rev-list --count HEAD --not --remotes", WorkingDir: "/pool/1", Head: head, LocalOnly: count, RemoteRefs: 2},
	}
}

func unobservableCommits(reason string) worktree.CommitSafetyObservation {
	return worktree.CommitSafetyObservation{
		State: worktree.CommitSafetyUnknown,
		Probe: worktree.CommitSafetyProbe{Command: "git rev-list --count HEAD --not --remotes", WorkingDir: "/pool/1", Reason: reason},
	}
}

func commitSafetyTask(t *testing.T, pr string, merged bool) (string, state.Attempt) {
	t.Helper()
	home := reconcileFixture(t)
	attempt, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Brief: "data/task-1/brief.md"}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr: state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"}, LaunchSubmittedAt: "2026-08-15T00:00:00Z", LaunchConfirmedAt: "2026-08-15T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.MarkAttemptRunning(home, "task-1", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if pr != "" {
		if err := state.SetTaskPR(home, "task-1", pr); err != nil {
			t.Fatal(err)
		}
	}
	if merged {
		if err := state.SetTaskMerge(home, "task-1", "2026-08-15T00:00:02Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.TerminalizeTaskAndAttempt(home, "task-1", attempt.ID, state.AttemptRunning, state.AttemptCompleted); err != nil {
		t.Fatal(err)
	}
	return home, attempt
}

type commitSafetyProbeCount struct {
	returns int
	commits int
}

func commitSafetyRuntime(t *testing.T, observation worktree.CommitSafetyObservation) (*Runtime, *commitSafetyProbeCount) {
	t.Helper()
	counts := &commitSafetyProbeCount{}
	r := reconcileRuntime(&healthyReconcileHerdr{}, nil)
	r.deps.worktree.observeCommits = func(path string) worktree.CommitSafetyObservation {
		counts.commits++
		if path != "/pool/1" {
			t.Fatalf("observeCommits(%q), want the recorded worktree", path)
		}
		return observation
	}
	r.deps.worktree.returnWithID = func(string, string, bool) error {
		counts.returns++
		return nil
	}
	r.deps.prMerged = observedMergedPR(true)
	r.deps.prHead = func(context.Context, string) ghutil.PRObservation {
		t.Fatal("prHead called without a stub")
		return ghutil.PRObservation{}
	}
	return r, counts
}

func reconcileNeedsRepair(t *testing.T, r *Runtime, home string) ReconcileResult {
	t.Helper()
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Outcome != reconcileOutcomeRepair {
		t.Fatalf("report = %+v, want a single needs-repair result", report)
	}
	return report.Results[0]
}

func reconcileConverges(t *testing.T, r *Runtime, home string) {
	t.Helper()
	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Outcome == reconcileOutcomeRepair {
		t.Fatalf("report = %+v, want reconciliation to converge without repair", report)
	}
}

func TestReconcileReturnsWorktreeWhenNoCommitIsLocalOnly(t *testing.T) {
	home, _ := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, remoteObservedCommits(commitSafetyHead))
	reconcileConverges(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if counts.returns != 1 || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Task.RepairCode != "" {
		t.Fatalf("returns=%d attempt=%+v task=%+v, want one proven return and no repair", counts.returns, history.Attempts[0], history.Task)
	}
}

func TestReconcileRefusesDirtyWorktreeBeforeAskingAboutCommits(t *testing.T) {
	home, _ := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, remoteObservedCommits(commitSafetyHead))
	r.deps.worktree.observeClean = func(string) (worktree.Cleanliness, error) { return worktree.Dirty, nil }
	reconcileNeedsRepair(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeDirty || counts.returns != 0 || counts.commits != 0 {
		t.Fatalf("task=%+v returns=%d commits=%d, want the existing dirty refusal ahead of any commit observation", history.Task, counts.returns, counts.commits)
	}
	if history.Attempts[0].TeardownWorktreeState != "" {
		t.Fatalf("worktree state = %q, want no cleanup recorded", history.Attempts[0].TeardownWorktreeState)
	}
}

func TestReconcileWithholdsReturnForOneUnpushedCommit(t *testing.T) {
	home, attempt := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 1))
	reconcileNeedsRepair(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeLocalCommits || history.Task.RepairAttemptID != attempt.ID || counts.returns != 0 {
		t.Fatalf("task=%+v returns=%d, want the local-only commit recorded against the attempt and no return", history.Task, counts.returns)
	}
	if history.Attempts[0].TeardownWorktreeState != "" {
		t.Fatalf("worktree state = %q, want no cleanup recorded", history.Attempts[0].TeardownWorktreeState)
	}
	if !strings.Contains(history.Task.RepairReason, "1 commit(s)") || !strings.Contains(history.Task.RepairReason, "no pull request is recorded") {
		t.Fatalf("repair reason = %q, want the count and the missing pull request named", history.Task.RepairReason)
	}
}

func TestReconcileWithholdsReturnForSeveralUnpushedCommits(t *testing.T) {
	home, _ := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 4))
	reconcileNeedsRepair(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeLocalCommits || counts.returns != 0 {
		t.Fatalf("task=%+v returns=%d, want the local-only commits recorded and no return", history.Task, counts.returns)
	}
	if !strings.Contains(history.Task.RepairReason, "4 commit(s)") {
		t.Fatalf("repair reason = %q, want all four commits counted", history.Task.RepairReason)
	}
}

func TestReconcileWithholdsReturnWhenCommitSafetyCannotBeObserved(t *testing.T) {
	cases := []struct {
		name        string
		pr          string
		observation worktree.CommitSafetyObservation
		prHead      func(context.Context, string) ghutil.PRObservation
		wantReason  string
	}{
		{
			name:        "no remote configured",
			observation: unobservableCommits("the clone holds no remote-tracking ref, so no commit here can be compared against one"),
			wantReason:  "holds no remote-tracking ref",
		},
		{
			name:        "pruned remote-tracking ref",
			observation: unobservableCommits("the branch records upstream origin/topic and no remote-tracking ref for it survives, so what the remote holds is no longer recorded here"),
			wantReason:  "no remote-tracking ref for it survives",
		},
		{
			name:        "github unreachable",
			pr:          "https://github.com/atqamz/hand/pull/7",
			observation: localOnlyCommits(commitSafetyHead, 1),
			prHead:      unobservedPR("gh pr view failed: dial tcp: lookup api.github.com: no such host"),
			wantReason:  "could not be read",
		},
		{
			// A pull request GitHub will not resolve is not a pull request proven to hold nothing:
			// absence answers the wrong question here, so it withholds the return like unknown.
			name:        "recorded pull request gh reports as absent",
			pr:          "https://github.com/atqamz/hand/pull/7",
			observation: localOnlyCommits(commitSafetyHead, 1),
			prHead:      absentPRObservation(),
			wantReason:  "could not be read",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := commitSafetyTask(t, tc.pr, false)
			r, counts := commitSafetyRuntime(t, tc.observation)
			if tc.prHead != nil {
				r.deps.prHead = tc.prHead
			}
			reconcileNeedsRepair(t, r, home)
			history, err := state.ReadHistory(home, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if history.Task.RepairCode != repairCodeWorktreeCommitSafetyUnknown || counts.returns != 0 {
				t.Fatalf("task=%+v returns=%d, want an unknown answer recorded and no return", history.Task, counts.returns)
			}
			if history.Attempts[0].TeardownWorktreeState != "" {
				t.Fatalf("worktree state = %q, want no cleanup recorded", history.Attempts[0].TeardownWorktreeState)
			}
			if !strings.Contains(history.Task.RepairReason, tc.wantReason) {
				t.Fatalf("repair reason = %q, want %q named so this cause is distinguishable", history.Task.RepairReason, tc.wantReason)
			}
			if strings.Contains(history.Task.RepairReason, "will not discard work") {
				t.Fatalf("repair reason = %q, must not record an unanswered question as work found at risk", history.Task.RepairReason)
			}
		})
	}
}

func TestReconcileReturnsWorktreeOfPushedAndMergedPullRequest(t *testing.T) {
	home, _ := commitSafetyTask(t, "https://github.com/atqamz/hand/pull/7", true)
	r, counts := commitSafetyRuntime(t, remoteObservedCommits(commitSafetyHead))
	reconcileConverges(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if counts.returns != 1 || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Task.RepairCode != "" {
		t.Fatalf("returns=%d attempt=%+v task=%+v, want ordinary cleanup of merged pushed work", counts.returns, history.Attempts[0], history.Task)
	}
}

func TestReconcileReturnsSquashMergedWorktreeOnPullRequestHeadEvidence(t *testing.T) {
	home, _ := commitSafetyTask(t, "https://github.com/atqamz/hand/pull/7", true)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 3))
	r.deps.prHead = func(_ context.Context, pr string) ghutil.PRObservation {
		if pr != "https://github.com/atqamz/hand/pull/7" {
			t.Fatalf("prHead(%q), want the recorded pull request", pr)
		}
		return ghutil.PRObservation{State: ghutil.ObservationFound, URL: pr, Head: commitSafetyHead}
	}
	reconcileConverges(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if counts.returns != 1 || history.Attempts[0].TeardownWorktreeState != state.TeardownResourceReleased || history.Task.RepairCode != "" {
		t.Fatalf("returns=%d attempt=%+v task=%+v, want the squashed branch returned on head evidence", counts.returns, history.Attempts[0], history.Task)
	}
}

func TestReconcileWithholdsReturnForCommitPastThePushedPullRequestHead(t *testing.T) {
	home, _ := commitSafetyTask(t, "https://github.com/atqamz/hand/pull/7", true)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 1))
	r.deps.prHead = observedPRHead("2222222222222222222222222222222222222222")
	reconcileNeedsRepair(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.RepairCode != repairCodeWorktreeLocalCommits || counts.returns != 0 {
		t.Fatalf("task=%+v returns=%d, want work past the pushed head withheld", history.Task, counts.returns)
	}
	if !strings.Contains(history.Task.RepairReason, "222222222222") || !strings.Contains(history.Task.RepairReason, "111111111111") {
		t.Fatalf("repair reason = %q, want both the pushed head and the local head named", history.Task.RepairReason)
	}
}

func TestReconcileKeepsAttemptTerminalWhileWorktreeReturnIsWithheld(t *testing.T) {
	home, attempt := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 2))
	client := &healthyReconcileHerdr{}
	r.deps.herdr = func() herdrClient { return client }
	reconcileNeedsRepair(t, r, home)
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt != nil || history.Attempts[0].Lifecycle != state.AttemptCompleted || history.Task.Lifecycle != state.TaskTerminal {
		t.Fatalf("history=%+v, want the Attempt still terminal while cleanup is withheld", history)
	}
	if history.Attempts[0].TeardownHerdrState != state.TeardownResourceReleased || client.closed != 1 {
		t.Fatalf("attempt=%+v closes=%d, want the Herdr resource released independently of the worktree", history.Attempts[0], client.closed)
	}
	if history.Task.RepairCode != repairCodeWorktreeLocalCommits || history.Task.RepairAttemptID != attempt.ID || counts.returns != 0 {
		t.Fatalf("task=%+v returns=%d, want the withheld worktree recorded against the terminal attempt", history.Task, counts.returns)
	}
}

func TestReconcileWithheldWorktreeReturnIsIdempotent(t *testing.T) {
	home, _ := commitSafetyTask(t, "", false)
	r, counts := commitSafetyRuntime(t, localOnlyCommits(commitSafetyHead, 2))
	reconcileNeedsRepair(t, r, home)
	first := durableSnapshot(t, home)
	r.deps.now = func() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }
	reconcileNeedsRepair(t, r, home)
	second := durableSnapshot(t, home)
	if !bytes.Equal(first, second) {
		t.Fatalf("durable state changed across reconciles:\nfirst  = %s\nsecond = %s", first, second)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.ActiveAttempt != nil || history.Attempts[0].Lifecycle != state.AttemptCompleted {
		t.Fatalf("history=%+v, want the Attempt still terminal after a second withheld pass", history)
	}
	if counts.returns != 0 || counts.commits != 2 {
		t.Fatalf("returns=%d commits=%d, want one observation per pass and no return", counts.returns, counts.commits)
	}
}

func durableSnapshot(t *testing.T, home string) []byte {
	t.Helper()
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
