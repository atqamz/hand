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

func storedEffort(effort string) string {
	if effort == "default" {
		return ""
	}
	return effort
}

func specText(s harness.Spec) string {
	return strings.Join(strings.Fields(s.Model+" "+s.Effort), " ")
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
		atPrompt := func() error {
			ag, err := c.Explain(ctx, a.PaneID)
			if err != nil {
				return runtimeErr(err)
			}
			if ag.Status != "idle" && ag.Status != "done" {
				return fmt.Errorf("%w: attempt %s is %s, not idle at its prompt; nothing changed; switch it once its turn ends", state.ErrConflict, ref, ag.Status)
			}
			return nil
		}
		if err := atPrompt(); err != nil {
			return err
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
			return fmt.Errorf("%w: attempt %s already runs %s", state.ErrConflict, ref, specText(prev))
		}
		if err := r.opencodeListed(*model); err != nil {
			return err
		}
		argv, err := harness.SupervisorArgv(bin, to, session, "", a.Worktree, true)
		if err != nil {
			return err
		}
		if err := atPrompt(); err != nil {
			return err
		}
		note, err := stopWorker(ctx, c, terminal(a.Terminal))
		if err != nil {
			return err
		}
		r.warn(note)
		r.warn("the worker's background shells and dev servers in its pane's process tree ended with it; a process that left that tree keeps running, and text it had typed but not sent is gone")
		pane := func(text, model, effort string) (state.Attempt, error) {
			term, err := c.Create(ctx, a.Worktree, "hand-"+ref, argv)
			if err != nil {
				if luvus.Code(err) == "" {
					_ = closeLabelled(ctx, c, "hand-"+ref, "")
				}
				return a, runtimeErr(err)
			}
			running, err := st.AttemptSwitched(ctx, id, state.Terminal{ServerGeneration: term.ServerGeneration, TerminalID: term.TerminalID, PaneID: term.PaneID, PID: term.Root.PID, StartMarker: term.Root.StartMarker}, text, model, effort)
			if err != nil {
				return a, errors.Join(err, stopErr(stopWorker(ctx, c, term)))
			}
			waitSettled(ctx, c, term.PaneID)
			return running, nil
		}
		setSession := func(s harness.Spec) error {
			return detached(ctx, func(ctx context.Context) error { return harness.OpencodeSwitch(ctx, bin, r.home, session, s) })
		}
		noPane := func(cause error) error {
			reason := fmt.Sprintf("switch to %s failed: %v", specText(to), cause)
			if _, endErr := st.EndAttempt(ctx, id, state.AttemptInterrupted, reason); endErr != nil {
				return errors.Join(cause, endErr)
			}
			return fmt.Errorf("%w; attempt %s has no pane and is interrupted, its worktree and opencode session %s stay; start it again on its branch with `hand attempt clean %s`, then `hand attempt start %s --continue %s`", cause, ref, session, ref, state.TaskRef(a.TaskID), ref)
		}
		if err := setSession(to); err != nil {
			back, sentence := "back on "+cmp.Or(specText(prev), "its old model"), "still on its old model"
			keep, keepEffort := a.Model, a.Effort
			now, verr := harness.OpencodeSessionModel(ctx, bin, r.home, session)
			switch {
			case verr != nil || now.Model != prev.Model || !sameVariant(now.Effort, prev.Effort):
				back, sentence = "back on its pane, model unverified", "its model is unverified: the watcher records the session's model on its next sweep"
			case now.Model != "":
				keep, keepEffort = now.Model, storedEffort(now.Effort)
			}
			if _, perr := pane(back, keep, keepEffort); perr != nil {
				return noPane(errors.Join(err, perr))
			}
			return fmt.Errorf("%w; attempt %s is back on its pane, %s", err, ref, sentence)
		}
		running, err := pane("switched to "+specText(to), *model, storedEffort(*effort))
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
