package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const (
	supervisorLabel  = "hand-supervisor"
	supervisorMarker = "You are supervisor "
	wakeHeader       = "[hand v1 wake]"
)

var supervisorCommands = map[string]handler{
	"start":     cmdSupervisorStart,
	"resume":    cmdSupervisorResume,
	"stop":      cmdSupervisorStop,
	"show":      cmdSupervisorShow,
	"keys":      cmdSupervisorKeys,
	"send":      cmdSupervisorSend,
	"interrupt": cmdSupervisorInterrupt,
	"switch":    cmdSupervisorSwitch,
	"force":     cmdSupervisorForce,
}

func init() {
	commands["supervisor"] = func(r *runner, args []string) error { return sub(r, args, "supervisor", supervisorCommands) }
}

func launchMarker(ref string) string { return supervisorMarker + ref + " of the Hand fleet " }

func (r *runner) supervisorLock(wait bool) (func(), bool, error) { return r.lock("supervisor", wait) }

func cmdSupervisorStart(r *runner, args []string) error {
	fs := flags("supervisor start")
	name := fs.String("harness", "", "claude, codex or opencode")
	model := fs.String("model", "", "model alias or name")
	effort := fs.String("effort", "", "reasoning effort")
	profile := fs.String("profile", "", "routing profile from routing.json")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	spec, err := r.routed("supervisor start", harness.Spec{Harness: *name, Model: *model, Effort: *effort}, *profile)
	if err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		unlock, _, err := r.supervisorLock(true)
		if err != nil {
			return err
		}
		defer unlock()
		last, ok, err := st.LatestSupervisor(ctx)
		if err != nil {
			return err
		}
		if spec == (harness.Spec{}) {
			if !ok {
				return usageError{"supervisor start: there is no earlier supervisor to copy; give --harness, --model and --effort, or --profile"}
			}
			spec = harness.Spec{Harness: last.Harness, Model: last.Model, Effort: last.Effort}
		}
		bin, err := supervisorBin(r, spec)
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		ref := state.SupervisorRef(last.ID + 1)
		cmd := r.env.command()
		prompt := launchMarker(ref) + r.fleet.Name + ". Follow AGENTS.md: run `" + cmd + " orient` now, then work from the operator's messages and from messages that start with " + wakeHeader + ". `" + cmd + "` is `" + exe + "`: when `" + cmd + "` is not on your PATH, run that path, and never run another `hand`."
		session := ""
		if spec.Harness == "claude" {
			session = harness.NewSessionID()
		}
		argv, err := harness.SupervisorArgv(bin, spec, session, prompt, false)
		if err != nil {
			return err
		}
		cursor, err := st.LastEventSeq(ctx)
		if err != nil {
			return err
		}
		sup, err := r.launchSupervisor(ctx, st, c, state.SupervisorSpec{Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: argv, Session: session, WakeCursor: cursor}, ref)
		if err != nil {
			return err
		}
		return r.reportLaunch(c, sup, true)
	})
}

func cmdSupervisorResume(r *runner, args []string) error {
	if _, err := parse(flags("supervisor resume"), args, 0); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		sup, err := r.resumeSupervisor(ctx, st, c, false)
		if err != nil {
			return err
		}
		return r.reportLaunch(c, sup, false)
	})
}

func (r *runner) resumeSupervisor(ctx context.Context, st *state.Store, c luvus.Client, auto bool) (state.Supervisor, error) {
	unlock, _, err := r.supervisorLock(true)
	if err != nil {
		return state.Supervisor{}, err
	}
	defer unlock()
	last, ok, err := st.LatestSupervisor(ctx)
	switch {
	case err != nil:
		return state.Supervisor{}, err
	case auto && (!ok || (last.Status != state.AttemptInterrupted && last.Status != state.AttemptExited)):
		return state.Supervisor{}, nil
	case !ok:
		return state.Supervisor{}, fmt.Errorf("%w: there is no supervisor to resume; start one: `hand supervisor start --harness H --model M --effort E`", state.ErrNotFound)
	case last.Live():
		return state.Supervisor{}, fmt.Errorf("%w: supervisor %s is %s; only an ended one can be resumed", state.ErrConflict, state.SupervisorRef(last.ID), last.Status)
	}
	if err := r.findSession(ctx, st, &last); err != nil {
		return state.Supervisor{}, err
	}
	if last.Session == "" {
		if auto {
			return state.Supervisor{}, nil
		}
		return state.Supervisor{}, fmt.Errorf("%w: supervisor %s has no known session to resume; start a new one: `hand supervisor start`", state.ErrConflict, state.SupervisorRef(last.ID))
	}
	spec := harness.Spec{Harness: last.Harness, Model: last.Model, Effort: last.Effort}
	if last.Switching() {
		spec.Model, spec.Effort = last.SwitchModel, last.SwitchEffort
	}
	return r.relaunch(ctx, st, c, last, spec)
}

func (r *runner) relaunch(ctx context.Context, st *state.Store, c luvus.Client, last state.Supervisor, spec harness.Spec) (state.Supervisor, error) {
	bin, err := supervisorBin(r, spec)
	if err != nil {
		return state.Supervisor{}, err
	}
	argv, err := harness.SupervisorArgv(bin, spec, last.Session, "", true)
	if err != nil {
		return state.Supervisor{}, err
	}
	sup, err := r.launchSupervisor(ctx, st, c, state.SupervisorSpec{Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: argv, Session: last.Session, WakeCursor: last.WakeCursor}, "")
	if err == nil {
		waitSettled(ctx, c, sup.PaneID)
	}
	return sup, err
}

func (r *runner) applySwitch(ctx context.Context, st *state.Store, c luvus.Client, sup state.Supervisor) (state.Supervisor, error) {
	spec := harness.Spec{Harness: sup.Harness, Model: sup.SwitchModel, Effort: sup.SwitchEffort}
	if _, err := supervisorBin(r, spec); err != nil {
		return sup, err
	}
	to := state.AttemptExited
	if rootAlive(sup.PID, sup.StartMarker) {
		if err := stopWorker(ctx, c, terminal(sup.Terminal)); err != nil {
			return sup, err
		}
		to = state.AttemptStopped
	}
	ended, err := st.EndSupervisor(ctx, sup.ID, to, "switched to "+sup.SwitchModel+" "+sup.SwitchEffort)
	if err != nil {
		return sup, err
	}
	sup, err = r.relaunch(ctx, st, c, ended, spec)
	if err != nil && !errors.Is(err, errLaunchUnknown) {
		err = fmt.Errorf("%w; supervisor %s stopped for the switch to %s %s, continue it with `hand supervisor resume`", err, state.SupervisorRef(ended.ID), spec.Model, spec.Effort)
	}
	return sup, err
}

func cmdSupervisorSwitch(r *runner, args []string) error {
	fs := flags("supervisor switch")
	model := fs.String("model", "", "model alias or name")
	effort := fs.String("effort", "", "reasoning effort")
	profile := fs.String("profile", "", "routing profile from routing.json")
	cancel := fs.Bool("cancel", false, "cancel the pending switch")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if *cancel == (*model != "" || *effort != "" || *profile != "") {
		return usageError{"supervisor switch: give --profile, or --model and --effort (either may be left out), or --cancel alone"}
	}
	routed, err := r.routed("supervisor switch", harness.Spec{Model: *model, Effort: *effort}, *profile)
	if err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
		sup, err := runningSupervisor(ctx, st)
		if err != nil {
			return err
		}
		ref := state.SupervisorRef(sup.ID)
		var d toon.Doc
		if *cancel {
			unlock, _, err := r.supervisorLock(true)
			if err != nil {
				return err
			}
			defer unlock()
			if sup, err = runningSupervisor(ctx, st); err != nil {
				return err
			}
			if _, err := st.CancelSupervisorSwitch(ctx, sup.ID); err != nil {
				return err
			}
			d.Field("supervisor", state.SupervisorRef(sup.ID))
			d.Field("switch", "canceled")
			return r.print(&d)
		}
		if sup.Harness == "opencode" {
			return fmt.Errorf("%w: opencode keeps its model in the session; switching is not supported", state.ErrInvalid)
		}
		spec := harness.Spec{Harness: sup.Harness, Model: sup.Model, Effort: sup.Effort}
		switch {
		case *profile != "" && routed.Harness != sup.Harness:
			return fmt.Errorf("%w: switch keeps harness %s; profile %s uses %s. Stop and start a new supervisor to change harness", state.ErrInvalid, sup.Harness, *profile, routed.Harness)
		case *profile != "":
			spec = routed
		default:
			spec.Model, spec.Effort = cmp.Or(*model, spec.Model), cmp.Or(*effort, spec.Effort)
		}
		if err := harness.Validate(spec, harness.CodexHome(r.env.Getenv)); err != nil {
			return err
		}
		if spec.Model == sup.Model && spec.Effort == sup.Effort {
			return fmt.Errorf("%w: supervisor %s already runs %s at effort %s", state.ErrConflict, ref, sup.Model, sup.Effort)
		}
		if err := r.findSession(ctx, st, &sup); err != nil {
			return err
		}
		if sup.Session == "" {
			return fmt.Errorf("%w: supervisor %s has no known session yet; switch once it has one", state.ErrConflict, ref)
		}
		if _, err := st.SetSupervisorSwitch(ctx, sup.ID, spec.Model, spec.Effort); err != nil {
			return err
		}
		why, err := r.deliver(ctx, st, c, caps, true)
		if err != nil {
			return err
		}
		now, _, err := st.LatestSupervisor(ctx)
		if err != nil {
			return err
		}
		d.Field("supervisor", state.SupervisorRef(now.ID))
		if now.ID != sup.ID {
			d.Field("switch", "applied")
			d.Field("model", now.Model)
			d.Field("effort", now.Effort)
			return r.print(&d)
		}
		d.Field("switch", "pending")
		d.Field("model", spec.Model)
		d.Field("effort", spec.Effort)
		if why != "" {
			d.Field("why", why)
		}
		d.Help("Cancel it: `hand supervisor switch --cancel`")
		return r.print(&d)
	})
}

const (
	settleWait  = 30 * time.Second
	settleQuiet = time.Second
)

func waitSettled(ctx context.Context, c luvus.Client, pane string) {
	ctx, cancel := context.WithTimeout(ctx, settleWait)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	rev, since := int64(-1), time.Time{}
	for {
		s, err := c.Read(ctx, pane, 60)
		switch {
		case luvus.Code(err) != "":
			return
		case err == nil && strings.TrimSpace(s.Text) != "" && s.ContentRevision != rev:
			rev, since = s.ContentRevision, time.Now()
		case err == nil && strings.TrimSpace(s.Text) != "" && time.Since(since) >= settleQuiet:
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func supervisorBin(r *runner, spec harness.Spec) (string, error) {
	if err := harness.Validate(spec, harness.CodexHome(r.env.Getenv)); err != nil {
		return "", err
	}
	return harness.LookPath(spec.Harness, r.env.Getenv("PATH"))
}

func (r *runner) launchSupervisor(ctx context.Context, st *state.Store, c luvus.Client, spec state.SupervisorSpec, want string) (state.Supervisor, error) {
	sup, err := st.AddSupervisor(ctx, spec)
	if err != nil {
		return sup, err
	}
	ref := state.SupervisorRef(sup.ID)
	fail := func(err error) (state.Supervisor, error) {
		if _, endErr := st.EndSupervisor(ctx, sup.ID, state.AttemptFailed, err.Error()); endErr != nil {
			return sup, errors.Join(err, endErr)
		}
		return sup, err
	}
	if want != "" && ref != want {
		return fail(fmt.Errorf("%w: the launch prompt names %s but the supervisor was numbered %s", state.ErrConflict, want, ref))
	}
	term, err := c.Create(ctx, r.home, supervisorLabel, spec.Argv)
	if err != nil {
		if luvus.Code(err) == "" {
			err = fmt.Errorf("%w: %w; supervisor %s stays launching, and sync fails it %s after it started", errLaunchUnknown, err, ref, launchGrace)
			return sup, errors.Join(err, closeLabelled(ctx, c, supervisorLabel, ""))
		}
		return fail(runtimeErr(err))
	}
	running, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: term.ServerGeneration, TerminalID: term.TerminalID, PaneID: term.PaneID, PID: term.Root.PID, StartMarker: term.Root.StartMarker})
	if err != nil {
		return sup, errors.Join(err, stopWorker(ctx, c, term))
	}
	return running, nil
}

func (r *runner) reportLaunch(c luvus.Client, running state.Supervisor, prompted bool) error {
	var d toon.Doc
	d.Field("supervisor", state.SupervisorRef(running.ID))
	d.Field("harness", running.Harness)
	d.Field("status", running.Status)
	d.Field("pane", running.PaneID)
	d.Field("session", running.Session)
	help := []string{"Check it: `hand supervisor show`", "Watch it live: `hand attach supervisor`"}
	if prompted && harness.Prefills(running.Harness) {
		sent, confirmed := submitPrefilled(r.ctx(), c, running.PaneID, running.TerminalID, supervisorMarker)
		switch {
		case confirmed:
			d.Field("prompt", "submitted")
		case sent:
			d.Field("prompt", "sent unconfirmed")
			help = append(help, "Enter was sent but the supervisor did not react: check `hand supervisor show`, and press Enter with `hand supervisor keys --revision N enter` only if the launch prompt is still in the input box")
		default:
			d.Field("prompt", "not submitted")
			help = append(help, "Press Enter once the launch prompt is on screen: `hand supervisor show`, then `hand supervisor keys --revision N enter`")
		}
	}
	d.Help(help...)
	return r.print(&d)
}

func runningSupervisor(ctx context.Context, st *state.Store) (state.Supervisor, error) {
	sup, ok, err := st.LiveSupervisor(ctx)
	switch {
	case err != nil:
		return sup, err
	case !ok:
		return sup, fmt.Errorf("%w: no supervisor is running; start one: `hand supervisor start`, or continue the last: `hand supervisor resume`", state.ErrConflict)
	case sup.Status != state.AttemptRunning:
		return sup, fmt.Errorf("%w: supervisor %s is %s", state.ErrConflict, state.SupervisorRef(sup.ID), sup.Status)
	}
	return sup, nil
}

func cmdSupervisorStop(r *runner, args []string) error {
	if _, err := parse(flags("supervisor stop"), args, 0); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		unlock, _, err := r.supervisorLock(true)
		if err != nil {
			return err
		}
		defer unlock()
		sup, err := runningSupervisor(ctx, st)
		if err != nil {
			return err
		}
		to, reason := state.AttemptExited, "root process already gone"
		if rootAlive(sup.PID, sup.StartMarker) {
			if err := stopWorker(ctx, c, terminal(sup.Terminal)); err != nil {
				return err
			}
			to, reason = state.AttemptStopped, "stopped by operator"
		}
		ended, err := st.EndSupervisor(ctx, sup.ID, to, reason)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("supervisor", state.SupervisorRef(ended.ID))
		d.Field("status", ended.Status)
		d.Field("reason", ended.Reason)
		d.Help("Continue it later: `hand supervisor resume`")
		return r.print(&d)
	})
}

func cmdSupervisorShow(r *runner, args []string) error {
	if _, err := parse(flags("supervisor show"), args, 0); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		sup, ok, err := st.LatestSupervisor(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: there is no supervisor yet; start one: `hand supervisor start --harness H --model M --effort E`", state.ErrNotFound)
		}
		pending, err := st.PendingSupervisorInputs(ctx)
		if err != nil {
			return err
		}
		origin, err := st.SupervisorOrigin(ctx, sup)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("supervisor", state.SupervisorRef(sup.ID))
		if origin != sup.ID {
			d.Field("resumes", state.SupervisorRef(origin))
		}
		d.Field("harness", strings.Join(slices.DeleteFunc([]string{sup.Harness, sup.Model, sup.Effort}, func(v string) bool { return v == "" }), " "))
		d.Field("status", sup.Status)
		if sup.Reason != "" {
			d.Field("reason", sup.Reason)
		}
		d.Field("session", sup.Session)
		if sup.Status == state.AttemptRunning {
			ag, err := c.Explain(ctx, sup.PaneID)
			if err != nil {
				return runtimeErr(err)
			}
			s, err := c.Read(ctx, sup.PaneID, 1)
			if err != nil {
				return runtimeErr(err)
			}
			d.Field("pane", sup.PaneID)
			d.Field("agent", ag.Status)
			if ag.Hint != "" {
				d.Field("blocked_on", ag.Hint)
				d.Help("Answer it: `hand supervisor keys --revision " + strconv.FormatInt(s.ContentRevision, 10) + " KEY...` with " + strings.Join(state.SupervisorKeys, ", "))
			}
			if s.TerminalID == sup.TerminalID {
				d.Field("revision", strconv.FormatInt(s.ContentRevision, 10))
			}
		}
		d.Field("pending", strconv.Itoa(len(pending)))
		if sup.Switching() {
			d.Field("switch", sup.SwitchModel+" "+sup.SwitchEffort)
		}
		d.Field("wake_cursor", strconv.FormatInt(sup.WakeCursor, 10))
		if !sup.Live() {
			d.Help("Continue it: `hand supervisor resume`, or start fresh: `hand supervisor start`")
		}
		return r.print(&d)
	})
}

func cmdSupervisorKeys(r *runner, args []string) error {
	fs := flags("supervisor keys")
	revision := fs.Int64("revision", -1, "content revision printed by `hand supervisor show`")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("supervisor keys: %v", err)}
	}
	if fs.NArg() == 0 || *revision < 0 {
		return usageError{"usage: hand supervisor keys --revision N KEY..."}
	}
	keys := fs.Args()
	for _, k := range keys {
		if !slices.Contains(state.SupervisorKeys, k) {
			return fmt.Errorf("%w: key %q is not allowed; use %s", state.ErrInvalid, k, strings.Join(state.SupervisorKeys, ", "))
		}
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		sup, err := runningSupervisor(ctx, st)
		if err != nil {
			return err
		}
		if err := c.Keys(ctx, sup.PaneID, keys, *revision, sup.TerminalID); err != nil {
			if luvus.Code(err) == "content_revision_conflict" {
				return fmt.Errorf("%w: the screen changed since revision %d; nothing was sent; check it again", state.ErrConflict, *revision)
			}
			return runtimeErr(err)
		}
		var d toon.Doc
		d.Field("supervisor", state.SupervisorRef(sup.ID))
		d.Field("keys", strings.Join(keys, " "))
		return r.print(&d)
	})
}

func (r *runner) settleSupervisor(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
	unlock, ok, err := r.supervisorLock(false)
	if err != nil || !ok {
		return err
	}
	defer unlock()
	sup, ok, err := st.LatestSupervisor(ctx)
	if err != nil || !ok {
		return err
	}
	if sup.Live() {
		to, reason, err := r.observeTerminal(ctx, c, caps, sup.Status, sup.CreatedAt, sup.Terminal)
		switch {
		case err != nil:
			return err
		case to != "":
			return r.endSupervisor(ctx, st, c, sup, to, reason)
		case sup.Status != state.AttemptRunning:
			return nil
		}
	}
	_ = r.closeSupervisorTerminals(ctx, st, c, sup, sup.Live())
	return nil
}

func (r *runner) closeSupervisorTerminals(ctx context.Context, st *state.Store, c luvus.Client, sup state.Supervisor, keep bool) error {
	sessions, err := st.SupervisorSessions(ctx)
	if err != nil {
		return err
	}
	homes := []string{r.home}
	if real, err := filepath.EvalSymlinks(r.home); err == nil && real != r.home {
		homes = append(homes, real)
	}
	return closeTerminals(ctx, c, func(t luvus.Terminal) bool {
		switch {
		case keep && t.TerminalID == sup.TerminalID:
			return false
		case t.Label == supervisorLabel || (sup.TerminalID != "" && t.TerminalID == sup.TerminalID):
			return true
		case len(sessions) == 0 || !slices.Contains(homes, t.CWD):
			return false
		}
		ag, err := c.Explain(ctx, t.PaneID)
		return err == nil && ag.Session != "" && slices.Contains(sessions, ag.Session)
	})
}

func (r *runner) endSupervisor(ctx context.Context, st *state.Store, c luvus.Client, sup state.Supervisor, to, reason string) error {
	if err := stopRoot(sup.PID, sup.StartMarker); err != nil {
		return err
	}
	if err := r.closeSupervisorTerminals(ctx, st, c, sup, false); err != nil {
		if sup.Status == state.AttemptLaunching {
			return nil
		}
		reason += "; terminal cleanup failed: " + err.Error()
	}
	if _, err := st.EndSupervisor(ctx, sup.ID, to, reason); err != nil && !errors.Is(err, state.ErrConflict) {
		return err
	}
	return nil
}

func (r *runner) findSession(ctx context.Context, st *state.Store, sup *state.Supervisor) error {
	if sup.Session != "" || sup.Harness == "claude" || sup.Status == state.AttemptLaunching {
		return nil
	}
	created, err := time.Parse(time.RFC3339Nano, sup.CreatedAt)
	if err != nil {
		return err
	}
	since := created.Truncate(time.Second)
	dir := r.home
	if real, err := filepath.EvalSymlinks(r.home); err == nil {
		dir = real
	}
	marker := launchMarker(state.SupervisorRef(sup.ID))
	var id string
	switch sup.Harness {
	case "codex":
		id, err = harness.CodexSession(harness.CodexHome(r.env.Getenv), dir, since, marker)
	case "opencode":
		var bin string
		if bin, err = harness.LookPath("opencode", r.env.Getenv("PATH")); err == nil {
			id, err = harness.OpencodeSession(bin, dir, since, marker)
		}
	}
	if err != nil || id == "" {
		return err
	}
	if err := st.SetSupervisorSession(ctx, sup.ID, id); err != nil {
		return err
	}
	sup.Session = id
	return nil
}

func cmdSupervisorSend(r *runner, args []string) error {
	fs := flags("supervisor send")
	text := fs.String("text", "", "message for the supervisor")
	file := fs.String("file", "", "file holding the message")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if (*text == "") == (*file == "") {
		return usageError{"supervisor send: give exactly one of --text or --file"}
	}
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("%w: %v", state.ErrInvalid, err)
		}
		*text = string(b)
	}
	if err := state.CheckMessage(*text); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
		in, err := st.AddSupervisorInput(ctx, *text)
		if err != nil {
			return err
		}
		why, err := r.deliver(ctx, st, c, caps, true)
		if err != nil {
			return err
		}
		pending, err := st.PendingSupervisorInputs(ctx)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("input", "i"+strconv.FormatInt(in.ID, 10))
		if !slices.ContainsFunc(pending, func(p state.SupervisorInput) bool { return p.ID == in.ID }) {
			d.Field("delivered", "yes")
			return r.print(&d)
		}
		if why == "" {
			why = "queued behind earlier messages"
		}
		d.Field("delivered", "no")
		d.Field("why", why)
		d.Help("It is delivered once the supervisor can take it; check with `hand supervisor show`")
		return r.print(&d)
	})
}

func (r *runner) deliver(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities, wait bool) (string, error) {
	unlock, ok, err := r.supervisorLock(wait)
	if err != nil {
		return "", err
	}
	if !ok {
		return "another delivery is running", nil
	}
	defer unlock()
	sup, ok, err := st.LiveSupervisor(ctx)
	if err != nil {
		return "", err
	}
	if !ok || sup.Status != state.AttemptRunning {
		return "no live supervisor", nil
	}
	ref := state.SupervisorRef(sup.ID)
	if sup.ServerGeneration != caps.ServerGeneration {
		return "supervisor " + ref + " belongs to an earlier luvus server", nil
	}
	if health, err := c.Validate(ctx, terminal(sup.Terminal)); err != nil || health == "gone" {
		return "supervisor " + ref + " terminal is gone", nil
	}
	if sup.Session == "" && sup.Harness != "claude" {
		if err := r.findSession(ctx, st, &sup); err != nil {
			return "cannot find the " + sup.Harness + " session: " + err.Error(), nil
		}
		if sup.Session == "" {
			return "waiting for " + sup.Harness + " to start its session; a trust or setup screen reads as idle", nil
		}
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return "supervisor status unknown: " + err.Error(), nil
	}
	switch ag.Status {
	case "idle", "done", "working":
	case "blocked":
		return "supervisor blocked: " + ag.Hint, nil
	default:
		return "supervisor is " + ag.Status, nil
	}
	if sup.Switching() {
		if ag.Status == "working" {
			return "switching to " + sup.SwitchModel + " " + sup.SwitchEffort + " after this turn", nil
		}
		if sup, err = r.applySwitch(ctx, st, c, sup); err != nil {
			return "", err
		}
		if ag, err = c.Explain(ctx, sup.PaneID); err != nil {
			return "supervisor status unknown: " + err.Error(), nil
		}
		if ag.Status != "idle" && ag.Status != "done" && ag.Status != "working" {
			return "supervisor " + state.SupervisorRef(sup.ID) + " is " + ag.Status + " after the switch", nil
		}
	}
	pending, err := st.PendingSupervisorInputs(ctx)
	if err != nil {
		return "", err
	}
	for _, in := range pending {
		if err := c.Prompt(ctx, sup.PaneID, in.Body); err != nil {
			switch luvus.Code(err) {
			case "":
				return "", runtimeErr(err)
			case "agent_not_ready":
				return "supervisor is not at a prompt", nil
			}
			return "luvus refused the message: " + err.Error(), nil
		}
		if err := st.DeliverSupervisorInput(ctx, in.ID); err != nil {
			return "", err
		}
	}
	if len(pending) > 0 || (ag.Status != "idle" && ag.Status != "done") {
		return "", nil
	}
	events, err := st.EventsAfter(ctx, sup.WakeCursor, wakeKinds, 50)
	if err != nil || len(events) == 0 {
		return "", err
	}
	digest, last := wakeDigest(events)
	if err := c.Prompt(ctx, sup.PaneID, digest); err != nil {
		if luvus.Code(err) == "" {
			return "", runtimeErr(err)
		}
		return "", nil
	}
	return "", st.AdvanceWakeCursor(ctx, sup.ID, last)
}

func wakeDigest(events []state.Event) (string, int64) {
	var b strings.Builder
	b.WriteString(wakeHeader)
	var last int64
	for _, e := range events {
		line := "\n" + e.Kind + " " + strings.Map(func(ch rune) rune {
			if unicode.IsControl(ch) {
				return ' '
			}
			return ch
		}, e.Detail)
		if b.Len()+len(line) > harness.MaxPromptBytes {
			if last != 0 {
				break
			}
			line = strings.ToValidUTF8(line[:harness.MaxPromptBytes-b.Len()], "")
		}
		b.WriteString(line)
		last = e.Seq
	}
	return b.String(), last
}

const escGap = 300 * time.Millisecond

func cmdSupervisorForce(r *runner, args []string) error {
	if _, err := parse(flags("supervisor force"), args, 0); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
		n, why, err := r.force(ctx, st, c, caps)
		if err != nil {
			return err
		}
		var d toon.Doc
		d.Field("typed", strconv.Itoa(n))
		if why != "" {
			d.Field("why", why)
		}
		return r.print(&d)
	})
}

func (r *runner) force(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) (int, string, error) {
	unlock, _, err := r.supervisorLock(true)
	if err != nil {
		return 0, "", err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	sup, err := runningSupervisor(ctx, st)
	if err != nil {
		return 0, "", err
	}
	if sup.ServerGeneration != caps.ServerGeneration {
		return 0, "", fmt.Errorf("%w: supervisor %s belongs to an earlier luvus server", state.ErrConflict, state.SupervisorRef(sup.ID))
	}
	if sup.Harness != "claude" {
		return 0, "", fmt.Errorf("%w: force types into Claude Code only; a %s supervisor waits for Luvus to read it as ready", state.ErrInvalid, sup.Harness)
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return 0, "", runtimeErr(err)
	}
	if ag.Status != "blocked" {
		unlock()
		locked = false
		why, err := r.deliver(ctx, st, c, caps, true)
		if why == "" {
			why = "the supervisor is " + ag.Status + ", so Hand delivered normally"
		}
		return 0, why, err
	}
	pending, err := st.PendingSupervisorInputs(ctx)
	if err != nil {
		return 0, "", err
	}
	for i, in := range pending {
		if err := typeIn(ctx, c, sup, in.Body); err != nil {
			if i > 0 {
				err = fmt.Errorf("%w (typed %d of %d queued messages first)", err, i, len(pending))
			}
			return i, "", err
		}
		if err := st.DeliverSupervisorInput(ctx, in.ID); err != nil {
			return i, "", err
		}
	}
	if len(pending) > 0 {
		return len(pending), "", nil
	}
	events, err := st.EventsAfter(ctx, sup.WakeCursor, wakeKinds, 50)
	if err != nil {
		return 0, "", err
	}
	if len(events) == 0 {
		return 0, "nothing waits for the supervisor", nil
	}
	digest, last := wakeDigest(events)
	if err := typeIn(ctx, c, sup, digest); err != nil {
		return 0, "", err
	}
	return 1, "", st.AdvanceWakeCursor(ctx, sup.ID, last)
}

func emptyPrompt(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if strings.TrimSpace(strings.ReplaceAll(line, "\u00a0", " ")) == "❯" {
			return true
		}
	}
	return false
}

func typeIn(ctx context.Context, c luvus.Client, sup state.Supervisor, text string) error {
	keys := make([]string, 0, len(text)+1)
	for _, ch := range text {
		switch ch {
		case '\n':
			keys = append(keys, `\`, "enter")
		case '\t':
			keys = append(keys, " ")
		default:
			keys = append(keys, string(ch))
		}
	}
	keys = append(keys, "enter")
	for range 3 {
		s, err := c.Read(ctx, sup.PaneID, 40)
		if err != nil {
			return runtimeErr(err)
		}
		if s.TerminalID != sup.TerminalID {
			return fmt.Errorf("%w: pane %s no longer holds supervisor %s; nothing was typed", state.ErrConflict, sup.PaneID, state.SupervisorRef(sup.ID))
		}
		if !emptyPrompt(s.Text) {
			return fmt.Errorf("%w: the supervisor's screen shows no empty prompt, so it may really be asking something; answer it with its keys", state.ErrConflict)
		}
		err = c.Keys(ctx, sup.PaneID, keys, s.ContentRevision, sup.TerminalID)
		if luvus.Code(err) != "content_revision_conflict" {
			if err != nil {
				return runtimeErr(err)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: the supervisor's screen kept changing; nothing was typed; try again", state.ErrConflict)
}

func cmdSupervisorInterrupt(r *runner, args []string) error {
	if _, err := parse(flags("supervisor interrupt"), args, 0); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		sup, err := runningSupervisor(ctx, st)
		if err != nil {
			return err
		}
		presses := 1
		if sup.Harness == "opencode" {
			presses = 2
		}
		for i := range presses {
			if i > 0 {
				time.Sleep(escGap)
			}
			if err := pressEsc(ctx, c, sup); err != nil {
				return err
			}
		}
		var d toon.Doc
		d.Field("supervisor", state.SupervisorRef(sup.ID))
		d.Field("keys", strings.TrimSpace(strings.Repeat("esc ", presses)))
		return r.print(&d)
	})
}

func pressEsc(ctx context.Context, c luvus.Client, sup state.Supervisor) error {
	for range 3 {
		s, err := c.Read(ctx, sup.PaneID, 1)
		if err != nil {
			return runtimeErr(err)
		}
		if s.TerminalID != sup.TerminalID {
			return fmt.Errorf("%w: pane %s now shows another terminal; nothing more was sent", state.ErrConflict, sup.PaneID)
		}
		err = c.Keys(ctx, sup.PaneID, []string{"esc"}, s.ContentRevision, sup.TerminalID)
		if err == nil {
			return nil
		}
		if luvus.Code(err) != "content_revision_conflict" {
			return runtimeErr(err)
		}
	}
	return fmt.Errorf("%w: the screen kept changing; nothing was sent; try again", state.ErrConflict)
}
