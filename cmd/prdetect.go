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

// detectPR made safe for a read command: it never fails the command, so a task with no branch, an
// unregistered or local-only project, a lock held elsewhere, an ambiguous branch, or a failed gh call all
// just leave t as read.
func detectPRForStatus(ctx context.Context, home string, t state.Task) state.Task {
	// A scout task never answers for a PR - its deliverable is data/<id>/report.md - and a torn-down task's
	// completion record is already written, so both skip the lookup rather than pay a forge round trip for a
	// PR recordPR would refuse. The scout half is the short-circuit checkLandedWork opens with.
	if t.PR != "" || t.Kind == state.KindScout || t.Lifecycle == state.TaskTerminal {
		return t
	}
	active, err := state.ActiveAttempt(home, t.ID)
	if err != nil {
		return t
	}
	proj, exists, err := project.Find(home, t.Project)
	if err != nil || !exists || proj.Mode == project.ModeLocalOnly {
		return t
	}

	// hand status holds no lock on the task, so it takes its own non-blocking one, mirroring the watcher's
	// own recordAutoPR.
	unlock, err := state.TryLock(home, "task:"+t.ID)
	if err != nil {
		return t
	}
	defer unlock()

	// Re-read under the lock in case a concurrent hand pr or teardown recorded a PR first.
	fresh, err := state.Read(home, t.ID)
	if err != nil {
		return t
	}
	if fresh.PR != "" {
		return fresh
	}

	ghCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Unlike hand teardown's landed-work guard, nothing here is gated on the answer, so an ambiguous branch
	// degrades like any other detection failure instead of refusing.
	detected, err := detectPR(ghCtx, home, fresh, active, proj)
	if err != nil {
		return t
	}
	return detected
}
