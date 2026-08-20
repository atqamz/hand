package cmd

import (
	"context"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
)

// Answers what GitHub reports for a task's PR, never the task itself: the caller renders a found
// answer as evidence rather than folding it into the durable pr field (ADR
// attention-is-one-derivation-over-three-channels.md, invariant 1). Empty where no lookup applies.
func detectPRForStatus(ctx context.Context, home string, history state.TaskHistory) ghutil.PRObservation {
	t := history.Task
	// A scout task never answers for a PR - its deliverable is data/<id>/report.md - and a torn-down task's
	// completion record is already written, so both skip the lookup rather than pay a forge round trip for a
	// PR recordPR would refuse. The scout half is the short-circuit checkLandedWork opens with.
	if t.PR != "" || t.Kind == state.KindScout || t.Lifecycle == state.TaskTerminal {
		return ghutil.PRObservation{}
	}
	if history.ActiveAttempt == nil {
		return ghutil.PRObservation{}
	}
	proj, exists, err := project.FindReadOnly(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return ghutil.PRObservation{}
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return runtime.DetectPRReadOnly(ghCtx, home, *history.ActiveAttempt, proj)
}
