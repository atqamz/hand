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
	"github.com/atqamz/hand/internal/gitcfg"
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
	bin, err := harnessBin(r, spec)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	text := string(prompt) + reportFooter(exe)
	argvFor := func(worktree string) ([]string, error) { return harness.Argv(bin, spec, text, worktree) }
	if err := harness.CheckPrompt(text); err != nil {
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
		g, err := repoGit(ctx, st, project)
		if err != nil {
			return err
		}
		if continues != 0 {
			prev, err := st.Attempt(ctx, continues)
			if err != nil {
				return err
			}
			if _, err := g.Git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+prev.Branch); err != nil {
				return fmt.Errorf("%w: branch %s of attempt %s no longer exists; start a fresh attempt with --base REF", state.ErrConflict, prev.Branch, state.AttemptRef(prev.ID))
			}
		}
		start, warning := *base, ""
		if start == "" && continues == 0 {
			start, warning = defaultBase(ctx, g)
		}
		a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: taskID, Harness: spec.Harness, Model: spec.Model, Effort: spec.Effort, ArgvFor: argvFor, Continues: continues}, fleet.Worktrees(r.root, r.fleet.ID))
		if err != nil {
			return err
		}
		running, err := launch(ctx, st, c, a, g, start)
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
		r.fieldWarn(&d)
		d.Field("pane", running.PaneID)
		help := []string{"Check it: `hand attempt show " + ref + "`", "Watch it live: `hand attach " + ref + "`"}
		fields, more, err := afterLaunch(r.ctx(), c, launched{
			harness: running.Harness, pane: running.PaneID, terminalID: running.TerminalID, worktree: running.Worktree,
			marker: reportMarker, thing: "briefing", who: "agent", repos: repoRoots(project.Repo),
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
	repos                                       []string
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
		trust, blocked, pressed, saved := acceptTrust(ctx, c, l.harness, l.pane, l.terminalID, l.worktree, l.repos)
		if len(pressed) > 0 {
			if err := note("keys", strings.Join(pressed, " ")); err != nil {
				return nil, nil, err
			}
		}
		fields = append(fields, [2]string{"trust", trust})
		if saved != "" {
			if err := note("trusted", saved); err != nil {
				return nil, nil, err
			}
			fields = append(fields, [2]string{"trusted", saved})
		}
		if blocked != "" {
			if err := note("blocked", blocked); err != nil {
				return nil, nil, err
			}
			help = append(help, "Read the screen: `"+l.screen+"`")
		}
	}
	return fields, help, nil
}

func repoRoots(repo string) []string {
	if real, err := filepath.EvalSymlinks(repo); err == nil && real != repo {
		return []string{repo, real}
	}
	return []string{repo}
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
	toYes     string
	folder    string
	repoNote  string
	wraps     bool
}

var trustScreens = map[string][]trustScreen{
	"agy": {{
		question:  "Do you trust the contents of this project?",
		yesCursor: "> Yes, I trust this folder",
		folder:    "Accessing workspace:",
	}},
	"claude": {{
		question:  "Quick safety check: Is this a project you created or one you trust?",
		yesCursor: "❯ Yes, I trust this folder",
		noCursor:  "❯ No, exit",
		toYes:     "down",
		folder:    "Accessing workspace:",
	}},
	"codex": {{
		question:  "Trust this folder?",
		yesCursor: "› 1. Trust and continue",
		noCursor:  "› 2. Quit",
		toYes:     "up",
		folder:    "Folder access",
		repoNote:  "Note:",
		wraps:     true,
	}, {
		question:  "Do you trust the contents of this directory?",
		yesCursor: "› 1. Yes, continue",
		noCursor:  "› 2. No, quit",
		toYes:     "up",
		folder:    "> You are in",
		repoNote:  "Note:",
		wraps:     true,
	}},
}

func askedScreen(screens []trustScreen, text string) (trustScreen, bool) {
	for _, s := range screens {
		if strings.Contains(text, s.question) {
			return s, true
		}
	}
	return trustScreen{}, false
}

func (s trustScreen) names(text string) (folder, repo string, noted bool) {
	text = unwrap(text)
	i := strings.Index(text, s.folder)
	if i < 0 {
		return "", "", false
	}
	text = text[i+len(s.folder):]
	end := strings.Index(text, s.question)
	if end < 0 {
		return "", "", false
	}
	if s.repoNote != "" {
		n, r := strings.Index(text, s.repoNote), strings.Index(text, "root:")
		noted = n >= 0 || r >= 0
		if n >= 0 && n < end {
			end = n
		}
		if r >= 0 {
			repo = text[r+len("root:"):]
			if q := strings.Index(repo, s.question); q >= 0 {
				repo = repo[:q]
			}
			repo = strings.TrimSpace(repo)
		}
	}
	return strings.TrimSpace(text[:end]), repo, noted
}

func (s trustScreen) same(shown, want string) bool {
	return samePath(shown, want) || (s.wraps && lostSpaces(canonPath(shown), canonPath(want)))
}

func (s trustScreen) hasYesRow(text string) bool {
	return strings.Contains(text, strings.TrimLeft(s.yesCursor, "›❯> "))
}

func (s trustScreen) alsoProject() string {
	if s.repoNote == "" {
		return ""
	}
	return " or project"
}

func samePath(a, b string) bool { return canonPath(a) == canonPath(b) }

func canonPath(p string) string {
	d := strings.TrimPrefix(p, `\\?\`)
	if len(d) < 3 || d[1] != ':' || (d[2] != '\\' && d[2] != '/') || !driveLetter(d[0]) {
		return p
	}
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(d, "/", `\`), `\`))
}

func driveLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func lostSpaces(shown, want string) bool {
	i := 0
	for j := 0; j < len(want); j++ {
		switch {
		case i < len(shown) && shown[i] == want[j]:
			i++
		case want[j] != ' ':
			return false
		}
	}
	return i == len(shown)
}

func acceptTrust(ctx context.Context, c luvus.Client, harness, pane, terminalID, worktree string, repos []string) (trust, note string, pressed []string, saved string) {
	screens, ok := trustScreens[harness]
	if !ok {
		return "not asked", "", nil, ""
	}
	wait, cancel := context.WithTimeout(ctx, trustWait)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	seen, last := false, int64(-1)
	for retried := false; ; {
		s, err := c.Read(wait, pane, 60)
		screen, asked := askedScreen(screens, s.Text)
		asked = err == nil && asked
		seen = seen || asked
		stable := err == nil && s.ContentRevision == last
		if err == nil {
			last = s.ContentRevision
		}
		onYes := asked && strings.Contains(s.Text, screen.yesCursor)
		onNo := asked && !onYes && screen.noCursor != "" && strings.Contains(s.Text, screen.noCursor)
		ready := onYes || (onNo && !slices.Contains(pressed, screen.toYes))
		switch {
		case luvus.Code(err) != "":
			return missed(harness, seen, pressed)
		case ready && len(pressed) == 0 && !stable:
			// Claude drops keys sent before it reads input; wait for a frame that stays.
		case ready:
			folder, shown, noted := screen.names(s.Text)
			matched := slices.IndexFunc(repos, func(r string) bool { return screen.same(shown, r) })
			if !screen.same(folder, worktree) || (noted && matched < 0) {
				return "not pressed", harness + " asks to trust a folder that is not its worktree" + screen.alsoProject() + "; check the screen", pressed, ""
			}
			key := "enter"
			if onNo {
				key = screen.toYes
			}
			err := c.Keys(wait, pane, []string{key}, s.ContentRevision, terminalID)
			switch {
			case err == nil && key != "enter":
				pressed = append(pressed, key)
				retried = false
				continue
			case err == nil && trustCleared(ctx, c, pane, screen.question, tick.C):
				return "accepted", "", append(pressed, key), savedTrust(harness, screen, worktree, repos, matched, true)
			case err == nil:
				return "accepted", harness + " trust screen did not clear; check the screen", append(pressed, key), savedTrust(harness, screen, worktree, repos, matched, false)
			case luvus.Code(err) != "content_revision_conflict" || retried:
				return "not pressed", trustUnpressed(harness), pressed, ""
			}
			retried = true
			continue
		}
		if !asked && strings.TrimSpace(s.Text) != "" {
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

func savedTrust(harness string, s trustScreen, worktree string, repos []string, matched int, cleared bool) string {
	if s.repoNote == "" {
		return ""
	}
	root := "this folder"
	if matched >= 0 {
		root = repos[matched]
	}
	if !cleared {
		return harness + ` was sent Enter on "` + s.question + `" for folder ` + worktree + `, and the screen stayed; if it saved the trust, that is for ` + root + ` in its config.toml`
	}
	return harness + ` accepted "` + s.question + `" for folder ` + worktree + `; it saves the trust for ` + root + ` in its config.toml`
}

func missed(harness string, seen bool, pressed []string) (trust, note string, keys []string, saved string) {
	if seen {
		return "not pressed", trustUnpressed(harness), pressed, ""
	}
	return "not asked", "", pressed, ""
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

func defaultBase(ctx context.Context, g gitcfg.Repo) (base, warning string) {
	if _, err := g.Git(ctx, "remote", "get-url", "origin"); err != nil {
		return "HEAD", ""
	}
	fctx, cancel := context.WithTimeout(ctx, fetchWait)
	_, err := g.Git(fctx, "fetch", "origin")
	cancel()
	remote := remoteDefault(ctx, g)
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
	tip, _ := g.Git(ctx, "log", "-1", "--format=%h, committed %cs", remote)
	return remote, "fetch failed (" + cause + "); using " + remote + " at " + strings.TrimSpace(tip)
}

func remoteDefault(ctx context.Context, g gitcfg.Repo) string {
	candidates := []string{"origin/main", "origin/master"}
	if head, err := g.Git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		candidates = slices.Insert(candidates, 0, strings.TrimSpace(head))
	}
	for _, ref := range candidates {
		if _, err := g.Git(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/"+ref); err == nil {
			return ref
		}
	}
	return ""
}

func launch(ctx context.Context, st *state.Store, c luvus.Client, a state.Attempt, g gitcfg.Repo, base string) (state.Attempt, error) {
	if err := makePrivate(filepath.Dir(a.Worktree)); err != nil {
		return a, err
	}
	add := []string{"worktree", "add", "--no-track", "-b", a.Branch, a.Worktree, base}
	if a.Continues != 0 {
		add = []string{"worktree", "add", a.Worktree, a.Branch}
	}
	if _, err := g.Git(ctx, add...); err != nil {
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
	if _, rmErr := g.Git(ctx, "worktree", "remove", "--force", a.Worktree); rmErr != nil {
		return a, errors.Join(err, rmErr)
	}
	if a.Continues != 0 {
		return a, err
	}
	if _, brErr := g.Git(ctx, "branch", "-D", a.Branch); brErr != nil {
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
		if screen, err := st.AttemptScreen(ctx, a.ID); err == nil && screen != "" {
			d.Text("last_screen", screen)
		}
		return r.print(&d)
	})
}
