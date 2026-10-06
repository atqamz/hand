package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	name := fs.String("harness", "", "claude, codex, opencode or agy")
	model := fs.String("model", "", "model alias or name")
	effort := fs.String("effort", "", "reasoning effort")
	promptFile := fs.String("prompt-file", "", "file holding the worker's briefing")
	base := fs.String("base", "", "git ref the worktree starts from (default origin/main, fetched first)")
	cont := fs.String("continue", "", "ended, cleaned attempt whose branch the worktree checks out")
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
	var continues int64
	if *cont != "" {
		if *base != "" {
			return usageError{"attempt start: --continue checks out an existing branch, so --base does not apply"}
		}
		if continues, err = parseID("a", *cont); err != nil {
			return err
		}
	}
	prompt, err := os.ReadFile(*promptFile)
	if err != nil {
		return fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	spec, err := r.routed("attempt start", harness.Spec{Harness: *name, Model: *model, Effort: *effort}, *profile)
	if err != nil {
		return err
	}
	if err := harness.Validate(spec, harness.EnvOf(r.env.Getenv)); err != nil {
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
		if continues != 0 {
			prev, err := st.Attempt(ctx, continues)
			if err != nil {
				return err
			}
			if _, err := git(ctx, project.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+prev.Branch); err != nil {
				return fmt.Errorf("%w: branch %s of attempt %s no longer exists; start a fresh attempt with --base REF", state.ErrConflict, prev.Branch, state.AttemptRef(prev.ID))
			}
		}
		start, warning := *base, ""
		if start == "" && continues == 0 {
			start, warning = defaultBase(ctx, project.Repo)
		}
		a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, Argv: argv, Continues: continues}, fleet.Worktrees(r.root, r.fleet.ID))
		if err != nil {
			return err
		}
		running, err := launch(ctx, st, c, a, project.Repo, start)
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
		if running.Continues != 0 {
			d.Field("continues", state.AttemptRef(running.Continues))
		}
		if warning != "" {
			d.Field("warning", warning)
		}
		d.Field("pane", running.PaneID)
		help := []string{"Check it: `hand attempt show " + ref + "`", "Watch it live: `hand attach " + ref + "`"}
		fields, more, err := afterLaunch(r.ctx(), c, launched{
			harness: running.Harness, pane: running.PaneID, terminalID: running.TerminalID, worktree: running.Worktree,
			marker: reportMarker, thing: "briefing", who: "agent",
			look: "hand attempt read " + ref, keys: "hand attempt keys --revision N " + ref + " enter", screen: "hand attempt read " + ref,
		}, func(kind, detail string) error { return st.NoteAttempt(ctx, running.ID, kind, detail) })
		if err != nil {
			return err
		}
		for _, f := range fields {
			d.Field(f[0], f[1])
		}
		help = append(help, more...)
		d.Help(help...)
		return r.print(&d)
	})
}

func (r *runner) routed(cmd string, spec harness.Spec, profile string) (harness.Spec, error) {
	if profile == "" {
		return spec, nil
	}
	if spec != (harness.Spec{}) {
		return spec, usageError{cmd + ": give either --profile or --harness/--model/--effort"}
	}
	if err := r.needHome(); err != nil {
		return spec, err
	}
	p, err := harness.LoadPolicy(r.home)
	if err != nil {
		return spec, err
	}
	return p.Profile(profile)
}

const (
	fetchWait   = 20 * time.Second
	prefillWait = 30 * time.Second
	submitTries = 3
)

var trustWait, trustConfirm, submitConfirm = 15 * time.Second, 5 * time.Second, 5 * time.Second

type launched struct {
	harness, pane, terminalID, worktree, marker string
	thing, who, look, keys, screen              string
}

func afterLaunch(ctx context.Context, c luvus.Client, l launched, note func(kind, detail string) error) (fields [][2]string, help []string, err error) {
	if harness.Prefills(l.harness) {
		sent, confirmed := submitPrefilled(ctx, c, l.pane, l.terminalID, l.marker)
		if sent {
			if err := note("keys", "enter"); err != nil {
				return nil, nil, err
			}
		}
		switch {
		case confirmed:
			fields = append(fields, [2]string{"prompt", "submitted"})
		case sent:
			if err := note("blocked", l.thing+" sent but not confirmed; check the screen"); err != nil {
				return nil, nil, err
			}
			fields = append(fields, [2]string{"prompt", "sent unconfirmed"})
			help = append(help, "Enter was sent but the "+l.who+" did not react: check `"+l.look+"`, and press Enter with `"+l.keys+"` only if the "+l.thing+" is still in the input box")
		default:
			if err := note("blocked", l.thing+" not submitted; press Enter"); err != nil {
				return nil, nil, err
			}
			fields = append(fields, [2]string{"prompt", "not submitted"})
			help = append(help, "Press Enter once the "+l.thing+" is on screen: `"+l.look+"`, then `"+l.keys+"`")
		}
	}
	if _, ok := trustScreens[l.harness]; ok {
		trust, blocked, pressed := acceptTrust(ctx, c, l.harness, l.pane, l.terminalID, l.worktree)
		if len(pressed) > 0 {
			if err := note("keys", strings.Join(pressed, " ")); err != nil {
				return nil, nil, err
			}
		}
		fields = append(fields, [2]string{"trust", trust})
		if blocked != "" {
			if err := note("blocked", blocked); err != nil {
				return nil, nil, err
			}
			help = append(help, "Read the screen: `"+l.screen+"`")
		}
	}
	return fields, help, nil
}

func submitPrefilled(ctx context.Context, c luvus.Client, pane, terminalID, marker string) (sent, confirmed bool) {
	ctx, cancel := context.WithTimeout(ctx, prefillWait)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for tries := 0; tries < submitTries; {
		s, err := c.Read(ctx, pane, 60)
		switch {
		case luvus.Code(err) != "":
			return sent, false
		case err == nil && strings.Contains(s.Text, marker) && idle(ctx, c, pane):
			err := c.Keys(ctx, pane, []string{"enter"}, s.ContentRevision, terminalID)
			if err == nil {
				sent, tries = true, tries+1
				if leftIdle(ctx, c, pane, tick.C) {
					return true, true
				}
				continue
			}
			if luvus.Code(err) != "content_revision_conflict" {
				return sent, false
			}
		}
		select {
		case <-ctx.Done():
			return sent, false
		case <-tick.C:
		}
	}
	return sent, false
}

type trustScreen struct {
	question  string
	yesCursor string
	noCursor  string
}

var trustScreens = map[string]trustScreen{
	"agy": {
		question:  "Do you trust the contents of this project?",
		yesCursor: "> Yes, I trust this folder",
	},
	"claude": {
		question:  "Quick safety check: Is this a project you created or one you trust?",
		yesCursor: "❯ Yes, I trust this folder",
		noCursor:  "❯ No, exit",
	},
}

func acceptTrust(ctx context.Context, c luvus.Client, harness, pane, terminalID, worktree string) (trust, note string, pressed []string) {
	screen, ok := trustScreens[harness]
	if !ok {
		return "not asked", "", nil
	}
	wait, cancel := context.WithTimeout(ctx, trustWait)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	own := "Accessing workspace:" + worktree + screen.question
	seen := false
	for retried := false; ; {
		s, err := c.Read(wait, pane, 60)
		asked := err == nil && strings.Contains(s.Text, screen.question)
		seen = seen || asked
		onYes := asked && strings.Contains(s.Text, screen.yesCursor)
		onNo := asked && !onYes && screen.noCursor != "" && strings.Contains(s.Text, screen.noCursor)
		switch {
		case luvus.Code(err) != "":
			return missed(harness, seen, pressed)
		case onYes || (onNo && !slices.Contains(pressed, "down")):
			if !strings.Contains(unwrap(s.Text), own) {
				return "not pressed", harness + " asks to trust a folder that is not its worktree; check the screen", pressed
			}
			key := "enter"
			if onNo {
				key = "down"
			}
			err := c.Keys(wait, pane, []string{key}, s.ContentRevision, terminalID)
			switch {
			case err == nil && key == "down":
				pressed = append(pressed, key)
				retried = false
				continue
			case err == nil && trustCleared(ctx, c, pane, screen.question, tick.C):
				return "accepted", "", append(pressed, key)
			case err == nil:
				return "accepted", harness + " trust screen did not clear; check the screen", append(pressed, key)
			case luvus.Code(err) != "content_revision_conflict" || retried:
				return "not pressed", trustUnpressed(harness), pressed
			}
			retried = true
			continue
		}
		if !asked {
			if ag, err := c.Explain(wait, pane); err == nil && ag.Status == "working" {
				return missed(harness, seen, pressed)
			}
		}
		select {
		case <-wait.Done():
			return missed(harness, seen, pressed)
		case <-tick.C:
		}
	}
}

func missed(harness string, seen bool, pressed []string) (trust, note string, keys []string) {
	if seen {
		return "not pressed", trustUnpressed(harness), pressed
	}
	return "not asked", "", pressed
}

func trustUnpressed(harness string) string {
	return harness + " trust screen was not pressed; check the screen"
}

func trustCleared(ctx context.Context, c luvus.Client, pane, question string, tick <-chan time.Time) bool {
	ctx, cancel := context.WithTimeout(ctx, trustConfirm)
	defer cancel()
	for {
		if s, err := c.Read(ctx, pane, 60); err == nil && !strings.Contains(s.Text, question) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick:
		}
	}
}

func unwrap(screen string) string {
	var b strings.Builder
	for _, line := range strings.Split(screen, "\n") {
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

func idle(ctx context.Context, c luvus.Client, pane string) bool {
	ag, err := c.Explain(ctx, pane)
	return err == nil && ag.Status == "idle"
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

func makePrivate(fleetDir string) error {
	if err := os.MkdirAll(fleetDir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, dir := range []string{fleetDir, filepath.Dir(fleetDir)} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func defaultBase(ctx context.Context, repo string) (base, warning string) {
	if _, err := git(ctx, repo, "remote", "get-url", "origin"); err != nil {
		return "HEAD", ""
	}
	fctx, cancel := context.WithTimeout(ctx, fetchWait)
	_, err := git(fctx, repo, "fetch", "origin")
	cancel()
	remote := remoteDefault(ctx, repo)
	switch {
	case err == nil && remote == "":
		return "HEAD", ""
	case err == nil:
		return remote, ""
	}
	cause := strings.Join(strings.Fields(err.Error()), " ")
	if remote == "" {
		return "HEAD", "fetch failed (" + cause + "); no remote-tracking branch to start from, using the local HEAD"
	}
	tip, _ := git(ctx, repo, "log", "-1", "--format=%h, committed %cs", remote)
	return remote, "fetch failed (" + cause + "); using " + remote + " at " + strings.TrimSpace(tip)
}

func remoteDefault(ctx context.Context, repo string) string {
	candidates := []string{"origin/main", "origin/master"}
	if head, err := git(ctx, repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		candidates = slices.Insert(candidates, 0, strings.TrimSpace(head))
	}
	for _, ref := range candidates {
		if _, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+ref); err == nil {
			return ref
		}
	}
	return ""
}

func launch(ctx context.Context, st *state.Store, c luvus.Client, a state.Attempt, repo, base string) (state.Attempt, error) {
	if err := makePrivate(filepath.Dir(a.Worktree)); err != nil {
		return a, err
	}
	add := []string{"worktree", "add", "-b", a.Branch, a.Worktree, base}
	if a.Continues != 0 {
		add = []string{"worktree", "add", a.Worktree, a.Branch}
	}
	if _, err := git(ctx, repo, add...); err != nil {
		return a, err
	}
	term, err := c.Create(ctx, a.Worktree, "hand-"+state.AttemptRef(a.ID), a.Argv)
	if err == nil {
		running, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: term.ServerGeneration, TerminalID: term.TerminalID, PaneID: term.PaneID, PID: term.Root.PID, StartMarker: term.Root.StartMarker})
		if err != nil {
			return a, errors.Join(err, stopErr(stopWorker(ctx, c, term)))
		}
		return running, nil
	}
	if luvus.Code(err) == "" {
		err = fmt.Errorf("%w: %w; attempt %s stays launching, and sync fails it %s after it started", errLaunchUnknown, err, state.AttemptRef(a.ID), launchGrace)
		return a, errors.Join(err, closeLabelled(ctx, c, "hand-"+state.AttemptRef(a.ID), a.TerminalID))
	}
	err = runtimeErr(err)
	if _, rmErr := git(ctx, repo, "worktree", "remove", "--force", a.Worktree); rmErr != nil {
		return a, errors.Join(err, rmErr)
	}
	if a.Continues != 0 {
		return a, err
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
		if a.Continues != 0 {
			d.Field("continues", state.AttemptRef(a.Continues))
		}
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
