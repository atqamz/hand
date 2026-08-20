package runtime

import (
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// LaunchConfirmedAt lands once a pane goes quiet or shows a harness's Ready signature, not once it
// starts working, so a read taken in the same instant would call an ordinary launch a stall. This
// outlasts that window by a wide margin, since only a genuine stall is worth a durable write.
const idleUnreportedGrace = 30 * time.Second

// Matches internal/watcher/usagelimit.go's own skew, so the two paths schedule identically against the
// same harness evidence.
const usageLimitSkew = time.Minute

// Mirrors internal/watcher/usagelimit.go's own bound: the refusal is the last thing a limited harness
// prints, so this only has to outlast whatever it draws under it.
const usageLimitReadLines = 60

// Herdr's signal for "stopped being busy" with nothing on the report channel explaining it: no report
// at all, or a last report still "working". Any other reported state already explains the pane going
// quiet, matching internal/watcher/events.go's ClassifyStatus and ClassifyCatchUp.
func idleUnreportedSinceLaunch(attempt state.Attempt, status herdr.Status) bool {
	return status.NotBusy() && (attempt.LastReportState == "" || attempt.LastReportState == state.ReportWorking)
}

// Whether enough time has passed since launch confirmation that an idle, unreported pane is evidence of
// a stall rather than the ordinary quiet moment before a harness's first turn begins. An unparseable or
// absent stamp answers false: unknown withholds, it never presumes a stall.
func launchSettled(attempt state.Attempt, now time.Time) bool {
	confirmed, err := time.Parse(time.RFC3339, attempt.LaunchConfirmedAt)
	if err != nil {
		return false
	}
	return now.Sub(confirmed) >= idleUnreportedGrace
}

// Keeps status_changed_at/for - the columns atqamz/hand#252's watch catch-up already reads - current
// with what Herdr just observed, so the fact survives whether or not any watcher process ever ran
// (atqamz/hand#259). Changes no lifecycle and releases no resource.
func (r *Runtime) recordAttemptLiveness(home string, task state.Task, attempt state.Attempt, status herdr.Status) (string, error) {
	idleUnreported := idleUnreportedSinceLaunch(attempt, status)
	if idleUnreported && !launchSettled(attempt, r.deps.now()) {
		return "", nil
	}

	statusChangedFor := string(status)
	freshStop := statusChangedFor != attempt.StatusChangedFor
	retryAt, attempts, episode := attempt.UsageLimitRetryAt, attempt.UsageLimitAttempts, attempt.UsageLimitEpisode
	if (status == herdr.StatusWorking || status == herdr.StatusBlocked) && attempt.UsageLimitRetryAt != "" {
		retryAt, attempts = "", 0
		if err := state.ClearHoldIfKind(home, task.ID, state.HoldKindLimit); err != nil {
			return "", fmt.Errorf("clear usage-limit hold for task %q: %w", task.ID, err)
		}
	}
	if idleUnreported && freshStop && attempt.UsageLimitRetryAt == "" && harness.SupportsUsageLimit(attempt.Harness) {
		if reset, limited := r.probeUsageLimit(attempt); limited {
			episode++
			attempts = 0
			retryAt = ""
			if !reset.IsZero() {
				retryAt = reset.Add(usageLimitSkew).UTC().Format(time.RFC3339)
			}
			if err := r.recordUsageLimitHold(home, task.ID, retryAt); err != nil {
				return "", err
			}
		}
	}

	liveness := statusChangedFor
	if idleUnreported {
		liveness = "idle-unreported"
	}
	if statusChangedFor == attempt.StatusChangedFor && retryAt == attempt.UsageLimitRetryAt && episode == attempt.UsageLimitEpisode {
		return liveness, nil
	}

	statusChangedAt := attempt.StatusChangedAt
	if statusChangedFor != attempt.StatusChangedFor {
		statusChangedAt = r.deps.now().UTC().Format(time.RFC3339)
	}
	if err := state.UpdateAttemptObservation(home, task.ID, attempt.ID, attempt.Lifecycle,
		statusChangedAt, statusChangedFor, false, attempt.LastReportState, attempt.LastReportNote,
		attempt.ParkedFiredFor, retryAt, attempts, episode, attempt.UsageLimitStuckEpisode); err != nil {
		return "", fmt.Errorf("record attempt %d liveness: %w", attempt.ID, err)
	}
	return liveness, nil
}

// Reads the stopped pane once for the harness's own catalogued quota-stop signature, the same
// per-harness capability internal/harness/usagelimit.go catalogues. This never steers the pane -
// reconcile does not implement the send protocol - it only leaves the fact durable for the next observer.
func (r *Runtime) probeUsageLimit(attempt state.Attempt) (time.Time, bool) {
	text, err := r.deps.herdr().PaneRead(attempt.Herdr.PaneID, usageLimitReadLines)
	if err != nil {
		return time.Time{}, false
	}
	return harness.DetectUsageLimit(attempt.Harness, text, r.deps.now())
}

// Projects the same fact internal/watcher/usagelimit.go's writeLimitHold does, so hand status shows a
// quota stop reconcile alone observed exactly as it would one the watcher observed.
func (r *Runtime) recordUsageLimitHold(home, taskID, retryAt string) error {
	reason := "harness stopped on a usage limit, observed by reconcile"
	if retryAt != "" {
		reason = fmt.Sprintf("harness stopped on a usage limit, observed by reconcile; next try no earlier than %s", retryAt)
	}
	if _, err := state.SetHoldIfNotOtherKind(home, state.Hold{
		ID: taskID, Kind: state.HoldKindLimit, Reason: reason, SetAt: r.deps.now().UTC().Format(time.RFC3339),
		Inferred: true,
	}); err != nil {
		return fmt.Errorf("record usage-limit hold for task %q: %w", taskID, err)
	}
	return nil
}
