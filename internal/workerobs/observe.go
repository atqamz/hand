// Package workerobs normalizes worker-process liveness from a persisted Attempt and live Herdr
// process evidence. It deliberately does not own Herdr resource ownership or task outcome.
package workerobs

import (
	"fmt"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

// ProcessInspector provides live process evidence for one task-owned pane.
type ProcessInspector interface {
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
}

// Normalize derives Worker identity and liveness without inferring task outcome.
// For one-shot workers, durable launch confirmation plus process absence means done liveness only.
func Normalize(attempt state.Attempt, pane herdr.Pane, inspector ProcessInspector) (herdr.Pane, error) {
	if !harness.IsOneShot(attempt.Harness) {
		return pane, nil
	}
	if pane.Agent != "" && pane.Agent != attempt.Harness {
		return pane, nil
	}
	if pane.PaneID == "" || inspector == nil {
		return pane, fmt.Errorf("one-shot worker process observation is unavailable")
	}
	processInfo, err := inspector.PaneProcessInfo(pane.PaneID)
	if err != nil {
		return pane, fmt.Errorf("observe one-shot worker process: %w", err)
	}
	if processInfo.HasExecutable(harness.Executable(attempt.Harness)) {
		pane.Agent = attempt.Harness
		pane.AgentStatus = herdr.StatusWorking
		return pane, nil
	}
	if attempt.LaunchConfirmedAt != "" {
		// "done" is Herdr liveness only: the one-shot is no longer resident. It is never task outcome.
		pane.Agent = attempt.Harness
		pane.AgentStatus = herdr.StatusDone
	}
	return pane, nil
}
