package cmd

import (
	"context"
	"time"

	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
)

func detectPR(ctx context.Context, home string, t state.Task, active state.Attempt, proj project.Project) (state.Task, error) {
	return runtime.DetectPR(ctx, home, t, active, proj)
}

func detectPRForStatus(ctx context.Context, home string, history state.TaskHistory) state.Task {
	t := history.Task
	// A scout task never answers for a PR - its deliverable is data/<id>/report.md - and a torn-down task's
	// completion record is already written, so both skip the lookup rather than pay a forge round trip for a
	// PR recordPR would refuse. The scout half is the short-circuit checkLandedWork opens with.
	if t.PR != "" || t.Kind == state.KindScout || t.Lifecycle == state.TaskTerminal {
		return t
	}
	if history.ActiveAttempt == nil {
		return t
	}
	proj, exists, err := project.FindReadOnly(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return t
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	detected, err := runtime.DetectPRReadOnly(ghCtx, home, t, *history.ActiveAttempt, proj)
	if err != nil {
		return t
	}
	return detected
}
