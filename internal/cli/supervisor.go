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
	"unicode"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const (
	supervisorLabel  = "hand-supervisor"
	supervisorMarker = "You are supervisor "
	wakeHeader       = "[hand v1 wake]"
	envelopePrefix   = "[hand v1"
)

var supervisorKeys = []string{"enter", "esc", "up", "down", "1", "2", "3"}

var supervisorCommands = map[string]handler{
	"start":  cmdSupervisorStart,
	"resume": cmdSupervisorResume,
	"stop":   cmdSupervisorStop,
	"show":   cmdSupervisorShow,
	"keys":   cmdSupervisorKeys,
	"send":   cmdSupervisorSend,
}

func init() {
	commands["supervisor"] = func(r *runner, args []string) error { return sub(r, args, "supervisor", supervisorCommands) }
}

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
		ref := state.SupervisorRef(last.ID + 1)
		prompt := fmt.Sprintf("You are supervisor %s of the Hand fleet %s. Follow AGENTS.md: run `%s orient` now, then work from the operator's messages and from messages that start with [hand v1 wake].", ref, r.fleet.Name, r.env.command())
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
	bin, err := supervisorBin(r, spec)
	if err != nil {
		return state.Supervisor{}, err
	}
	argv, err := harness.SupervisorArgv(bin, spec, last.Session, "", true)
	if err != nil {
		return state.Supervisor{}, err
	}
	return r.launchSupervisor(ctx, st, c, state.SupervisorSpec{Harness: last.Harness, Model: last.Model, Effort: last.Effort, Argv: argv, Session: last.Session, WakeCursor: last.WakeCursor}, "")
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
	help := []string{"Check it: `hand supervisor show`", "Watch it live: `luvus session attach " + fleet.Session(r.fleet.ID) + "`"}
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
		var d toon.Doc
		d.Field("supervisor", state.SupervisorRef(sup.ID))
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
				d.Help("Answer it: `hand supervisor keys --revision " + strconv.FormatInt(s.ContentRevision, 10) + " KEY...` with " + strings.Join(supervisorKeys, ", "))
			}
			if s.TerminalID == sup.TerminalID {
				d.Field("revision", strconv.FormatInt(s.ContentRevision, 10))
			}
		}
		d.Field("pending", strconv.Itoa(len(pending)))
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
		if !slices.Contains(supervisorKeys, k) {
			return fmt.Errorf("%w: key %q is not allowed; use %s", state.ErrInvalid, k, strings.Join(supervisorKeys, ", "))
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
	running := sup.Status == state.AttemptRunning
	switch {
	case !sup.Live():
		err = closeLabelled(ctx, c, supervisorLabel, "")
	default:
		to, reason, oerr := r.observeTerminal(ctx, c, caps, sup.Status, sup.CreatedAt, sup.Terminal)
		switch {
		case oerr != nil:
			return oerr
		case to != "":
			err = r.endSupervisor(ctx, st, c, sup, to, reason)
		case running:
			err = closeTerminals(ctx, c, func(t luvus.Terminal) bool { return t.Label == supervisorLabel && t.TerminalID != sup.TerminalID })
		}
	}
	if err != nil {
		return err
	}
	if running {
		_ = r.findSession(ctx, st, &sup)
	}
	return nil
}

func (r *runner) endSupervisor(ctx context.Context, st *state.Store, c luvus.Client, sup state.Supervisor, to, reason string) error {
	if err := stopRoot(sup.PID, sup.StartMarker); err != nil {
		return err
	}
	if err := closeLabelled(ctx, c, supervisorLabel, sup.TerminalID); err != nil {
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
	var id string
	switch sup.Harness {
	case "codex":
		id, err = harness.CodexSession(harness.CodexHome(r.env.Getenv), dir, since)
	case "opencode":
		var bin string
		if bin, err = harness.LookPath("opencode", r.env.Getenv("PATH")); err == nil {
			id, err = harness.OpencodeSession(bin, dir, since)
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
	if err := checkMessage(*text); err != nil {
		return err
	}
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		in, err := st.AddSupervisorInput(ctx, *text)
		if err != nil {
			return err
		}
		why, err := r.deliver(ctx, st, c, true)
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

func checkMessage(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("%w: message must not be empty", state.ErrInvalid)
	case len(text) > harness.MaxPromptBytes:
		return fmt.Errorf("%w: message is %d bytes; the most is %d", state.ErrInvalid, len(text), harness.MaxPromptBytes)
	case !utf8.ValidString(text):
		return fmt.Errorf("%w: message is not valid UTF-8", state.ErrInvalid)
	case strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), envelopePrefix):
		return fmt.Errorf("%w: message must not start with %q, which Hand keeps for its own envelopes", state.ErrInvalid, envelopePrefix)
	}
	for _, ch := range text {
		if unicode.IsControl(ch) && ch != '\n' && ch != '\t' {
			return fmt.Errorf("%w: message holds control character %U; only newlines and tabs are allowed", state.ErrInvalid, ch)
		}
	}
	return nil
}

func (r *runner) deliver(ctx context.Context, st *state.Store, c luvus.Client, wait bool) (string, error) {
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
	pending, err := st.PendingSupervisorInputs(ctx)
	if err != nil {
		return "", err
	}
	for _, in := range pending {
		if err := c.Prompt(ctx, sup.PaneID, in.Body); err != nil {
			if luvus.Code(err) == "agent_not_ready" {
				return "supervisor is not at a prompt", nil
			}
			return "", runtimeErr(err)
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
		if luvus.Code(err) == "agent_not_ready" {
			return "", nil
		}
		return "", runtimeErr(err)
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
