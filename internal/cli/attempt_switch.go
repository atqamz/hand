package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/transcript"
)

func init() {
	attemptCommands["switch"] = cmdAttemptSwitch
}

func sameVariant(a, b string) bool {
	return cmp.Or(a, "default") == cmp.Or(b, "default")
}

func cmdAttemptSwitch(r *runner, args []string) error {
	fs := flags("attempt switch")
	model := fs.String("model", "", "opencode model as provider/id")
	effort := fs.String("effort", "", "opencode variant of the model")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt switch: %v", err)}
	}
	pos := fs.Args()
	if len(pos) > 0 {
		if err := fs.Parse(pos[1:]); err != nil {
			return usageError{fmt.Sprintf("attempt switch: %v", err)}
		}
		pos = append(pos[:1:1], fs.Args()...)
	}
	if len(pos) != 1 || *model == "" {
		return usageError{"usage: hand attempt switch aN --model provider/id [--effort VARIANT]"}
	}
	id, err := parseID("a", pos[0])
	if err != nil {
		return err
	}
	to := harness.Spec{Harness: "opencode", Model: *model, Effort: *effort}
	if _, err := harness.Check(to, harness.EnvOf(r.env.Getenv)); err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		unlock, _, err := r.attemptLock(id, true)
		if err != nil {
			return err
		}
		defer unlock()
		a, err := runningAttempt(ctx, st, id)
		if err != nil {
			return err
		}
		ref := state.AttemptRef(id)
		session := argvSession(a.Argv)
		if a.Harness != "opencode" || session == "" {
			return fmt.Errorf("%w: attempt %s runs %s, and only an opencode attempt with a Hand session can switch its model", state.ErrConflict, ref, a.Harness)
		}
		bin, err := harnessBin(r, to)
		if err != nil {
			return err
		}
		ag, err := c.Explain(ctx, a.PaneID)
		if err != nil {
			return runtimeErr(err)
		}
		if ag.Status != "idle" {
			return fmt.Errorf("%w: attempt %s is %s, not idle at its prompt; nothing changed; switch it once its turn ends", state.ErrConflict, ref, ag.Status)
		}
		_, busy, _, err := transcript.OpencodeSessions(ctx, bin, r.home, r.env.Now(), longTurn)
		if err != nil {
			return fmt.Errorf("%w: cannot read opencode's session list (%v); nothing changed", state.ErrConflict, err)
		}
		if busy[session] {
			return fmt.Errorf("%w: opencode still runs a turn of attempt %s; nothing changed; switch it once its turn ends", state.ErrConflict, ref)
		}
		prev, err := harness.OpencodeSessionModel(ctx, bin, r.home, session)
		if err != nil {
			return fmt.Errorf("%w: cannot read the model of opencode session %s (%v); nothing changed", state.ErrConflict, session, err)
		}
		if prev.Model == *model && sameVariant(prev.Effort, *effort) {
			return fmt.Errorf("%w: attempt %s already runs %s", state.ErrConflict, ref, strings.Join(strings.Fields(prev.Model+" "+prev.Effort), " "))
		}
		if prev.Model != *model {
			if err := r.opencodeListed(*model); err != nil {
				return err
			}
		}
		argv, err := harness.SupervisorArgv(bin, to, session, "", a.Worktree, true)
		if err != nil {
			return err
		}
		warn, _ := harness.Check(to, harness.EnvOf(r.env.Getenv))
		r.warn(warn)
		if _, err := stopWorker(ctx, c, terminal(a.Terminal)); err != nil {
			return err
		}
		r.warn("the worker's background shells and dev servers ended with its pane, and text it had typed but not sent is gone")
		pane := func(text string) (state.Attempt, error) {
			term, err := c.Create(ctx, a.Worktree, "hand-"+ref, argv)
			if err != nil {
				if luvus.Code(err) == "" {
					_ = closeLabelled(ctx, c, "hand-"+ref, "")
				}
				return a, runtimeErr(err)
			}
			running, err := st.AttemptSwitched(ctx, id, state.Terminal{ServerGeneration: term.ServerGeneration, TerminalID: term.TerminalID, PaneID: term.PaneID, PID: term.Root.PID, StartMarker: term.Root.StartMarker}, text)
			if err != nil {
				return a, errors.Join(err, stopErr(stopWorker(ctx, c, term)))
			}
			return running, nil
		}
		setSession := func(s harness.Spec) error {
			return detached(ctx, func(ctx context.Context) error { return harness.OpencodeSwitch(ctx, bin, r.home, session, s) })
		}
		noPane := func(cause error) error {
			reason := fmt.Sprintf("switch to %s failed: %v", strings.Join(strings.Fields(*model+" "+*effort), " "), cause)
			if _, endErr := st.EndAttempt(ctx, id, state.AttemptInterrupted, reason); endErr != nil {
				return errors.Join(cause, endErr)
			}
			return fmt.Errorf("%w; attempt %s has no pane and is interrupted, its worktree and opencode session %s stay; start it again on its branch with `hand attempt clean %s`, then `hand attempt start %s --continue %s`", cause, ref, session, ref, state.TaskRef(a.TaskID), ref)
		}
		if err := setSession(to); err != nil {
			if _, perr := pane("back on " + strings.Join(strings.Fields(prev.Model+" "+prev.Effort), " ")); perr != nil {
				return noPane(errors.Join(err, perr))
			}
			return fmt.Errorf("%w; attempt %s is back on its pane, still on its old model", err, ref)
		}
		running, err := pane("switched to " + strings.Join(strings.Fields(*model+" "+*effort), " "))
		if err != nil {
			clause := fmt.Sprintf("opencode session %s stays on %s", session, *model)
			if prev.Model != "" {
				if rerr := setSession(prev); rerr != nil {
					clause = fmt.Sprintf("could not put opencode session %s back on %s: %v; it stays on %s", session, prev.Model, rerr, *model)
				} else {
					clause = ""
				}
			}
			if clause != "" {
				r.warn(clause)
				err = fmt.Errorf("%w; %s", err, clause)
			}
			return noPane(err)
		}
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("status", running.Status)
		d.Field("model", *model)
		if *effort != "" {
			d.Field("effort", *effort)
		}
		d.Field("pane", running.PaneID)
		r.fieldWarn(&d)
		d.Help("Check it: `hand attempt show "+ref+"`", "The watcher reads the model back from the session on its next sweep")
		return r.print(&d)
	})
}
