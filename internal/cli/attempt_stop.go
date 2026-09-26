package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const (
	stopGrace = 5 * time.Second
	killGrace = 2 * time.Second
)

func init() {
	attemptCommands["stop"] = cmdAttemptStop
	attemptCommands["clean"] = cmdAttemptClean
}

func rootAlive(pid int, marker string) bool {
	m, err := luvus.ProcStartMarker(pid)
	return err == nil && m == marker
}

func waitGone(pid int, marker string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !rootAlive(pid, marker) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !rootAlive(pid, marker)
}

func alreadyClosed(err error) bool {
	switch luvus.Code(err) {
	case "stale_terminal", "stale_route", "terminal_gone":
		return true
	}
	return false
}

func cmdAttemptStop(r *runner, args []string) error {
	pos, err := parse(flags("attempt stop"), args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("a", pos[0])
	if err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		a, err := runningAttempt(ctx, st, id)
		if err != nil {
			return err
		}
		to, reason := state.AttemptExited, "root process already gone"
		if rootAlive(a.PID, a.StartMarker) {
			_ = syscall.Kill(-a.PID, syscall.SIGTERM)
			if !waitGone(a.PID, a.StartMarker, stopGrace) {
				_ = syscall.Kill(-a.PID, syscall.SIGKILL)
				waitGone(a.PID, a.StartMarker, killGrace)
			}
			if err := c.Close(ctx, terminal(a)); err != nil && !alreadyClosed(err) {
				return runtimeErr(err)
			}
			to, reason = state.AttemptStopped, "stopped by operator"
		}
		ended, err := st.EndAttempt(ctx, id, to, reason)
		if err != nil {
			return err
		}
		ref := state.AttemptRef(id)
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("status", ended.Status)
		d.Field("reason", ended.Reason)
		d.Help("Remove its worktree when done with it: `hand attempt clean " + ref + "`")
		return r.print(&d)
	})
}

func cmdAttemptClean(r *runner, args []string) error {
	set := flags("attempt clean")
	discard := set.Bool("discard", false, "remove the worktree even with uncommitted changes")
	pos, err := parse(set, args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("a", pos[0])
	if err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		a, err := st.Attempt(ctx, id)
		if err != nil {
			return err
		}
		ref := state.AttemptRef(id)
		switch {
		case a.Live():
			return fmt.Errorf("%w: attempt %s is %s; stop it first", state.ErrConflict, ref, a.Status)
		case a.CleanedAt != "":
			return fmt.Errorf("%w: attempt %s is already cleaned", state.ErrConflict, ref)
		}
		task, err := st.Task(ctx, a.TaskID)
		if err != nil {
			return err
		}
		project, err := st.Project(ctx, task.Project)
		if err != nil {
			return err
		}
		if err := removeWorktree(ctx, c, project.Repo, a.Worktree, *discard); err != nil {
			return err
		}
		if _, err := st.CleanAttempt(ctx, id); err != nil {
			return err
		}
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("removed", a.Worktree)
		d.Help("Branch " + a.Branch + " is kept; delete it with `git -C " + project.Repo + " branch -D " + a.Branch + "` once merged")
		return r.print(&d)
	})
}

func removeWorktree(ctx context.Context, c luvus.Client, repo, dir string, discard bool) error {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		_, err := git(ctx, repo, "worktree", "prune")
		return err
	} else if err != nil {
		return err
	}
	dirty, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" && !discard {
		return fmt.Errorf("%w: worktree %s has uncommitted changes; commit them or pass --discard", state.ErrConflict, dir)
	}
	if err := closeTerminalsUnder(ctx, c, dir); err != nil {
		return err
	}
	rm := []string{"worktree", "remove"}
	if discard {
		rm = append(rm, "--force")
	}
	_, err = git(ctx, repo, append(rm, dir)...)
	return err
}

func closeTerminalsUnder(ctx context.Context, c luvus.Client, dir string) error {
	roots := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		roots = append(roots, real)
	}
	terms, err := c.Inventory(ctx)
	if err != nil {
		return runtimeErr(err)
	}
	for _, t := range terms {
		inside := false
		for _, root := range roots {
			inside = inside || t.CWD == root || strings.HasPrefix(t.CWD, root+string(filepath.Separator))
		}
		if !inside {
			continue
		}
		if err := c.Close(ctx, t); err != nil && !alreadyClosed(err) {
			return runtimeErr(err)
		}
	}
	return nil
}
