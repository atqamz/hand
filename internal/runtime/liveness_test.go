package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// One Herdr fake whose observed agent_status and pane scrollback are settable, so a case can drive
// working, idle-unreported and usage-limit evidence from the same client atqamz/hand#259 needs reconcile
// to read.
type livenessHerdr struct {
	healthyReconcileHerdr
	agent    string
	status   herdr.Status
	paneText string
}

func (f *livenessHerdr) PaneGet(string) (herdr.Pane, error) {
	agent := f.agent
	if agent == "" {
		agent = "claude"
	}
	return herdr.Pane{PaneID: "pane-1", TabID: "tab-1", WorkspaceID: "ws-1", Agent: agent, AgentStatus: f.status}, nil
}

func (f *livenessHerdr) PaneRead(string, int) (string, error) {
	return f.paneText, nil
}

// The fixed clock repairRuntime's Runtime carries; LaunchConfirmedAt is set relative to it so a case
// can choose whether the grace window has elapsed.
const livenessNow = "2026-08-15T00:00:00Z"

func livenessFixture(t *testing.T, launchConfirmedAt string, client herdrClient) (string, *Runtime, state.Attempt) {
	t.Helper()
	home, attempt := repairFixture(t, state.Task{}, state.Attempt{
		Lifecycle: state.AttemptProvisioning, Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr:             state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
		LaunchSubmittedAt: "2026-08-14T23:00:00Z", LaunchConfirmedAt: launchConfirmedAt,
	})
	repairMarkRunning(t, home, attempt)
	return home, repairRuntime(client), attempt
}

func TestReconcileRecordsIdleUnreportedOnceLaunchHasSettled(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle}
	home, r, attempt := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(report.Results) != 1 || report.Results[0].Liveness != "idle-unreported" {
		t.Fatalf("results = %+v, want liveness idle-unreported", report.Results)
	}
	if report.Results[0].Outcome != reconcileOutcomeHealthy {
		t.Fatalf("outcome = %q, want healthy: idle-unreported is not a repair diagnosis", report.Results[0].Outcome)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Attempts[0]
	if got.Lifecycle != state.AttemptRunning || got.StatusChangedFor != string(herdr.StatusIdle) || got.StatusChangedAt == "" {
		t.Fatalf("attempt after reconcile = %+v, want a running attempt with its idle status durably recorded", got)
	}
	if got.Worktree != attempt.Worktree || got.LeaseID != attempt.LeaseID || got.Herdr != attempt.Herdr {
		t.Fatalf("attempt resources = %+v, want them untouched by a liveness observation", got)
	}
}

func TestReconcileConvergesDonePaneWithoutWorkerReport(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusDone}
	home, r, attempt := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Landing != string(landingUnlanded) {
		t.Fatalf("result = %+v, want unlanded landing evidence", report.Results[0])
	}

	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskTerminal || history.ActiveAttempt != nil {
		t.Fatalf("history = %+v, want terminal task with no active attempt", history)
	}
	if got := history.Attempts[0].Lifecycle; got != state.AttemptInterrupted {
		t.Fatalf("attempt lifecycle = %q, want interrupted", got)
	}
	record, found, err := completion.FindAttempt(home, attempt.ID)
	if err != nil || !found || record.AttemptLifecycle != string(state.AttemptInterrupted) {
		t.Fatalf("completion = %+v found=%t err=%v, want interrupted completion evidence", record, found, err)
	}
}

func TestReconcileKeepsDonePaneWithTerminalWorkerReport(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusDone}
	home, r, attempt := livenessFixture(t, "2026-08-14T23:59:00Z", client)
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: checks green\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Lifecycle != state.TaskOpen || history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
		t.Fatalf("history = %+v, want running task preserved by terminal worker report", history)
	}
}

func TestReconcileWithholdsIdleUnreportedBeforeLaunchSettles(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle}
	// LaunchConfirmedAt equals the runtime's clock: no grace has elapsed yet.
	home, r, _ := livenessFixture(t, livenessNow, client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Liveness != "" {
		t.Fatalf("liveness = %q, want none: the harness has not had time to leave the launch quiet window yet", report.Results[0].Liveness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].StatusChangedFor != "" {
		t.Fatalf("status_changed_for = %q, want no durable write before the grace window elapses", history.Attempts[0].StatusChangedFor)
	}
}

func TestReconcileRecordsWorkingLiveness(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusWorking}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Liveness != "working" {
		t.Fatalf("liveness = %q, want working", report.Results[0].Liveness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].StatusChangedFor != string(herdr.StatusWorking) {
		t.Fatalf("status_changed_for = %q, want %q durably recorded", history.Attempts[0].StatusChangedFor, herdr.StatusWorking)
	}
}

// A report already on the channel explains the pane going quiet - paused, blocked, needs-decision, done
// or failed - so it is never idle-unreported whatever Herdr says, matching internal/watcher/events.go's
// own ClassifyStatus/ClassifyCatchUp rule.
func TestReconcileDoesNotFlagIdleAsUnreportedWhenAReportExplainsIt(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle}
	home, r, attempt := livenessFixture(t, "2026-08-14T23:59:00Z", client)
	if err := state.UpdateAttemptObservation(home, "task-1", attempt.ID, state.AttemptRunning,
		"", "", false, state.ReportPaused, "waiting on operator", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Liveness != string(herdr.StatusIdle) {
		t.Fatalf("liveness = %q, want the plain observed status since a report already explains the stop", report.Results[0].Liveness)
	}
}

func claudeUsageLimitText(withReset bool) string {
	if withReset {
		return "Usage limit reached. Your limit will reset at 3:00pm (UTC)."
	}
	return "Usage limit reached."
}

// atqamz/hand#259 test 2: a harness that exits on a quota error is recorded as such, with the stated
// retry time preserved when the harness supplies one - reachable from reconcile alone, with no watcher
// ever having run.
func TestReconcileRecordsUsageLimitWithStatedRetryTime(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(true)}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Liveness != "idle-unreported" {
		t.Fatalf("liveness = %q, want idle-unreported", report.Results[0].Liveness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Attempts[0]
	const wantRetryAt = "2026-08-15T15:01:00Z"
	if got.UsageLimitRetryAt != wantRetryAt || got.UsageLimitEpisode != 1 {
		t.Fatalf("attempt = %+v, want the stated reset time (plus skew) preserved as usage_limit_retry_at and episode 1", got)
	}
	hold, found, err := state.ReadHold(home, "task-1")
	if err != nil || !found || hold.Kind != state.HoldKindLimit || !strings.Contains(hold.Reason, wantRetryAt) {
		t.Fatalf("hold=%+v found=%t err=%v, want a limit hold naming the retry time", hold, found, err)
	}
	if !hold.Inferred {
		t.Fatalf("hold.Inferred = false, want true: this conclusion came from a pane scrape, not direct evidence")
	}
	if got.Lifecycle != state.AttemptRunning || got.Worktree != "/pool/1" || got.LeaseID != "lease-1" {
		t.Fatalf("attempt = %+v, want the running attempt and its resources left exactly as they were", got)
	}
}

func TestReconcileClearsInferredUsageLimitOnWorkingObservation(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(true)}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #1 = %v", err)
	}
	if _, found, err := state.ReadHold(home, "task-1"); err != nil || !found {
		t.Fatalf("ReadHold() after pane observation = found %t, err %v, want an inferred limit hold", found, err)
	}

	client.status = herdr.StatusWorking
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #2 = %v", err)
	}
	if _, found, err := state.ReadHold(home, "task-1"); err != nil || found {
		t.Fatalf("ReadHold() after working observation = found %t, err %v, want no hold", found, err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := history.Attempts[0]; got.UsageLimitRetryAt != "" || got.UsageLimitAttempts != 0 {
		t.Fatalf("attempt usage-limit state = %q/%d, want both cleared", got.UsageLimitRetryAt, got.UsageLimitAttempts)
	}
}

func TestReconcileClearsInferredUsageLimitWithoutRetryTime(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(false)}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #1 = %v", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := history.Attempts[0]; got.UsageLimitRetryAt != "" || got.UsageLimitAttempts != 0 {
		t.Fatalf("attempt usage-limit state = %q/%d, want no retry time or attempts", got.UsageLimitRetryAt, got.UsageLimitAttempts)
	}

	client.status = herdr.StatusWorking
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #2 = %v", err)
	}
	if _, found, err := state.ReadHold(home, "task-1"); err != nil || found {
		t.Fatalf("ReadHold() after working observation = found %t, err %v, want no hold", found, err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := history.Attempts[0]; got.UsageLimitRetryAt != "" || got.UsageLimitAttempts != 0 {
		t.Fatalf("attempt usage-limit state after working observation = %q/%d, want both cleared", got.UsageLimitRetryAt, got.UsageLimitAttempts)
	}
}

// The other half of test 2: when the harness's own refusal names no reset instant, reconcile records the
// limit without inventing a retry time - it never guesses what the harness did not supply.
func TestReconcileRecordsUsageLimitWithoutInventingARetryTime(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(false)}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Attempts[0]
	if got.UsageLimitRetryAt != "" || got.UsageLimitEpisode != 1 {
		t.Fatalf("attempt = %+v, want the limit recorded with no retry time invented", got)
	}
}

// A stated reset time isn't the only way a limit's stop edge becomes durable: without one,
// status_changed_for is what remembers this same idle pane was already probed, so a second reconcile
// on the still-idle attempt must not treat it as a fresh stop and bump the episode counter again.
func TestReconcileDoesNotReprobeTheSameStopWithoutAStatedRetryTime(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(false)}
	home, r, _ := livenessFixture(t, "2026-08-14T23:59:00Z", client)

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #1 = %v", err)
	}
	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() #2 = %v", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Attempts[0]
	if got.UsageLimitRetryAt != "" || got.UsageLimitEpisode != 1 {
		t.Fatalf("attempt = %+v, want the episode left at 1 on a repeated reconcile of the same stop", got)
	}
}

// Once a limit's retry schedule is already durable, reconcile leaves it to whichever mechanism is
// managing the backoff - most likely hand watch - rather than re-probing and re-bumping the episode
// counter underneath it.
func TestReconcileDoesNotReprobeAnAlreadyScheduledUsageLimit(t *testing.T) {
	client := &livenessHerdr{status: herdr.StatusIdle, paneText: claudeUsageLimitText(true)}
	home, r, attempt := livenessFixture(t, "2026-08-14T23:59:00Z", client)
	if err := state.UpdateAttemptObservation(home, "task-1", attempt.ID, state.AttemptRunning,
		"", "", false, "", "", "", "2026-08-16T00:00:00Z", 2, 5, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := history.Attempts[0]
	if got.UsageLimitRetryAt != "2026-08-16T00:00:00Z" || got.UsageLimitEpisode != 5 || got.UsageLimitAttempts != 2 {
		t.Fatalf("attempt = %+v, want the existing schedule left exactly as it was", got)
	}
}

// A harness with no catalogued usage-limit signature - every harness but claude and codex today -
// gets no pane read and no attempted detection; only status_changed_for durably changes.
func TestReconcileSkipsUsageLimitDetectionForAnUncataloguedHarness(t *testing.T) {
	client := &livenessHerdr{agent: "grok", status: herdr.StatusIdle, paneText: claudeUsageLimitText(true)}
	home, attempt := repairFixture(t, state.Task{}, state.Attempt{
		Lifecycle: state.AttemptProvisioning, Harness: "grok", Worktree: "/pool/1", LeaseID: "lease-1",
		Herdr:             state.Herdr{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1"},
		LaunchSubmittedAt: "2026-08-14T23:00:00Z", LaunchConfirmedAt: "2026-08-14T23:59:00Z",
	})
	repairMarkRunning(t, home, attempt)
	r := repairRuntime(client)

	report, err := r.Reconcile(ReconcileRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if report.Results[0].Liveness != "idle-unreported" {
		t.Fatalf("liveness = %q, want idle-unreported even with no usage-limit capability", report.Results[0].Liveness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Attempts[0].UsageLimitRetryAt != "" || history.Attempts[0].UsageLimitEpisode != 0 {
		t.Fatalf("attempt = %+v, want no usage-limit fact recorded for a harness with no catalogued signature", history.Attempts[0])
	}
}
