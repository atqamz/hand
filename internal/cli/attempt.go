package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var errLaunchUnknown = errors.New("launch outcome unknown")

var attemptCommands = map[string]handler{
	"start": cmdAttemptStart,
	"list":  cmdAttemptList,
	"show":  cmdAttemptShow,
}

func init() {
	commands["attempt"] = func(r *runner, args []string) error { return sub(r, args, "attempt", attemptCommands) }
}

func cmdAttemptStart(r *runner, args []string) error {
	fs := flags("attempt start")
	name := fs.String("harness", "", "claude, codex or opencode")
	model := fs.String("model", "", "model alias or name")
	effort := fs.String("effort", "", "reasoning effort")
	promptFile := fs.String("prompt-file", "", "file holding the worker's briefing")
	base := fs.String("base", "HEAD", "git ref the worktree starts from")
	profile := fs.String("profile", "", "routing profile from routing.json")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	taskID, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	if *promptFile == "" {
		return usageError{"attempt start: --prompt-file is required"}
	}
	prompt, err := os.ReadFile(*promptFile)
	if err != nil {
		return fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	spec := harness.Spec{Harness: *name, Model: *model, Effort: *effort}
	if *profile != "" {
		if spec != (harness.Spec{}) {
			return usageError{"attempt start: give either --profile or --harness/--model/--effort"}
		}
		if err := r.needHome(); err != nil {
			return err
		}
		p, err := harness.LoadPolicy(r.home)
		if err != nil {
			return err
		}
		if spec, err = p.Profile(*profile); err != nil {
			return err
		}
	}
	if err := harness.Validate(spec, harness.CodexHome(r.env.Getenv)); err != nil {
		return err
	}
	bin, err := harness.LookPath(spec.Harness, r.env.Getenv("PATH"))
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	argv, err := harness.Argv(bin, spec, string(prompt)+reportFooter(exe))
	if err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		task, err := st.Task(ctx, taskID)
		if err != nil {
			return err
		}
		project, err := st.Project(ctx, task.Project)
		if err != nil {
			return err
		}
		a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: argv}, fleet.Worktrees(r.root, r.fleet.ID))
		if err != nil {
			return err
		}
		running, err := launch(ctx, st, c, a, project.Repo, *base)
		if errors.Is(err, errLaunchUnknown) {
			return err
		}
		if err != nil {
			if _, endErr := st.EndAttempt(ctx, a.ID, state.AttemptFailed, err.Error()); endErr != nil {
				return errors.Join(err, endErr)
			}
			return err
		}
		ref := state.AttemptRef(running.ID)
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("task", state.TaskRef(running.TaskID))
		d.Field("status", running.Status)
		d.Field("worktree", running.Worktree)
		d.Field("branch", running.Branch)
		d.Field("pane", running.PaneID)
		help := []string{"Check it: `hand attempt show " + ref + "`", "Watch it live: `luvus session attach " + fleet.Session(r.fleet.ID) + "`"}
		if harness.Prefills(running.Harness) {
			if submitPrefilled(r.ctx(), c, running) {
				if err := st.NoteAttempt(ctx, running.ID, "keys", "enter"); err != nil {
					return err
				}
				d.Field("prompt", "submitted")
			} else {
				if err := st.NoteAttempt(ctx, running.ID, "blocked", "briefing not submitted; press Enter"); err != nil {
					return err
				}
				d.Field("prompt", "not submitted")
				help = append(help, "Press Enter once the briefing is on screen: `hand attempt read "+ref+"`, then `hand attempt keys --revision N "+ref+" enter`")
			}
		}
		d.Help(help...)
		return r.print(&d)
	})
}

const (
	prefillWait   = 30 * time.Second
	submitConfirm = 5 * time.Second
)

func submitPrefilled(ctx context.Context, c luvus.Client, a state.Attempt) bool {
	ctx, cancel := context.WithTimeout(ctx, prefillWait)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		s, err := c.Read(ctx, a.PaneID, 60)
		switch {
		case luvus.Code(err) != "":
			return false
		case err == nil && strings.Contains(s.Text, reportMarker):
			err := c.Keys(ctx, a.PaneID, []string{"enter"}, s.ContentRevision, a.TerminalID)
			if err == nil {
				return leftIdle(ctx, c, a.PaneID, tick.C)
			}
			if luvus.Code(err) != "content_revision_conflict" {
				return false
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func leftIdle(ctx context.Context, c luvus.Client, pane string, tick <-chan time.Time) bool {
	deadline := time.After(submitConfirm)
	for {
		if ag, err := c.Explain(ctx, pane); err == nil && ag.Status != "idle" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-tick:
		}
	}
}

func launch(ctx context.Context, st *state.Store, c luvus.Client, a state.Attempt, repo, base string) (state.Attempt, error) {
	if err := os.MkdirAll(filepath.Dir(a.Worktree), 0o755); err != nil {
		return a, err
	}
	if _, err := git(ctx, repo, "worktree", "add", "-b", a.Branch, a.Worktree, base); err != nil {
		return a, err
	}
	term, err := c.Create(ctx, a.Worktree, "hand-"+state.AttemptRef(a.ID), a.Argv)
	if err == nil {
		running, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: term.ServerGeneration, TerminalID: term.TerminalID, PaneID: term.PaneID, PID: term.Root.PID, StartMarker: term.Root.StartMarker})
		if err != nil {
			return a, errors.Join(err, stopWorker(ctx, c, term))
		}
		return running, nil
	}
	if luvus.Code(err) == "" {
		err = fmt.Errorf("%w: %w; attempt %s stays launching, and sync fails it %s after it started", errLaunchUnknown, err, state.AttemptRef(a.ID), launchGrace)
		return a, errors.Join(err, closeAttemptTerminals(ctx, c, a))
	}
	err = runtimeErr(err)
	if _, rmErr := git(ctx, repo, "worktree", "remove", "--force", a.Worktree); rmErr != nil {
		return a, errors.Join(err, rmErr)
	}
	if _, brErr := git(ctx, repo, "branch", "-D", a.Branch); brErr != nil {
		return a, errors.Join(err, brErr)
	}
	return a, err
}

func cmdAttemptList(r *runner, args []string) error {
	fs := flags("attempt list")
	task := fs.String("task", "", "only this task's attempts")
	limit := fs.Int("limit", 20, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var taskID int64
	if *task != "" {
		id, err := parseID("t", *task)
		if err != nil {
			return err
		}
		taskID = id
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, _ luvus.Client) error {
		as, err := st.Attempts(ctx, taskID, *limit)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(as))
		for _, a := range as {
			rows = append(rows, []string{state.AttemptRef(a.ID), state.TaskRef(a.TaskID), a.Harness, a.Model, a.Status})
		}
		var d toon.Doc
		d.Rows("attempts", []string{"id", "task", "harness", "model", "status"}, rows)
		return r.print(&d)
	})
}

func cmdAttemptShow(r *runner, args []string) error {
	pos, err := parse(flags("attempt show"), args, 1)
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
		ref := state.AttemptRef(a.ID)
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("task", state.TaskRef(a.TaskID))
		d.Field("harness", strings.Join(slices.DeleteFunc([]string{a.Harness, a.Model, a.Effort}, func(v string) bool { return v == "" }), " "))
		d.Field("status", a.Status)
		if a.Reason != "" {
			d.Field("reason", a.Reason)
		}
		d.Field("worktree", a.Worktree)
		d.Field("branch", a.Branch)
		d.Field("launch", strings.Join(a.Argv[:len(a.Argv)-1], " "))
		d.Field("prompt_bytes", strconv.Itoa(len(a.Argv[len(a.Argv)-1])))
		if a.Status == state.AttemptRunning {
			ag, err := c.Explain(ctx, a.PaneID)
			if err != nil {
				return runtimeErr(err)
			}
			d.Field("pane", a.PaneID)
			d.Field("agent", ag.Status)
			if ag.Hint != "" {
				d.Field("blocked_on", ag.Hint)
				d.Help("See the screen: `hand attempt read " + ref + "`")
			}
		}
		if a.CleanedAt != "" {
			d.Field("cleaned_at", a.CleanedAt)
		}
		return r.print(&d)
	})
}
