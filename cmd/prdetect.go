package cmd

import (
	"context"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
)

// Fills an empty PR field from GitHub for the status views, and reports what the lookup answered so a
// still-empty field is not read as an absence. The state is empty where no lookup applies at all, which
// is not the same as an absence either.
func detectPRForStatus(ctx context.Context, home string, history state.TaskHistory) (state.Task, ghutil.ObservationState) {
	t := history.Task
	// A scout task never answers for a PR - its deliverable is data/<id>/report.md - and a torn-down task's
	// completion record is already written, so both skip the lookup rather than pay a forge round trip for a
	// PR recordPR would refuse. The scout half is the short-circuit checkLandedWork opens with.
	if t.PR != "" || t.Kind == state.KindScout || t.Lifecycle == state.TaskTerminal {
		return t, ""
	}
	if history.ActiveAttempt == nil {
		return t, ""
	}
	proj, exists, err := project.FindReadOnly(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return t, ""
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	detected, observation := runtime.DetectPRReadOnly(ghCtx, home, t, *history.ActiveAttempt, proj)
	return detected, observation.State
}
