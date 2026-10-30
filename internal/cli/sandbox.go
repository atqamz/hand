package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/sandbox"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var closeOnExec = sandbox.CloseOnExec

var sandboxCommands = map[string]handler{
	"exec":  cmdSandboxExec,
	"check": cmdSandboxCheck,
}

func init() {
	commands["sandbox"] = func(r *runner, args []string) error { return sub(r, args, "sandbox", sandboxCommands) }
}

func cmdSandboxExec(r *runner, args []string) error {
	sep := slices.Index(args, "--")
	head, argv := args, []string(nil)
	if sep >= 0 {
		head, argv = args[:sep], args[sep+1:]
	}
	fs := flags("sandbox exec")
	printOnly := fs.Bool("print", false, "show the resolved policy and run nothing")
	attempt := fs.String("attempt", "", "attempt id; the policy then covers its fleet and harness")
	worktree := fs.String("worktree", "", "worktree to confine; default: the current folder")
	harness := fs.String("harness", "", "harness whose login and config folders are granted")
	access := fs.String("access", "", "open (no confinement) or boxed; required")
	if err := fs.Parse(head); err != nil {
		return usageError{fmt.Sprintf("sandbox exec: %v", err)}
	}
	if sep < 0 && !*printOnly {
		return usageError{"sandbox exec: give the command after --"}
	}
	if len(argv) == 0 && !*printOnly {
		return usageError{"sandbox exec: give a command after --"}
	}
	if *access == "" {
		return usageError{"sandbox exec: give --access open or boxed"}
	}
	if *attempt != "" && *worktree != "" {
		return usageError{"sandbox exec: give --attempt or --worktree, not both"}
	}
	if !sandbox.ValidMode(*access) {
		return usageError{fmt.Sprintf("sandbox exec: --access must be one of %s", strings.Join(sandbox.Modes, ", "))}
	}
	var bin string
	if len(argv) > 0 {
		var err error
		bin, err = exec.LookPath(argv[0])
		if err != nil {
			return fmt.Errorf("%w: %v", state.ErrInvalid, err)
		}
	}
	wt, spec, err := r.sandboxTarget(*attempt, *worktree, *harness)
	if err != nil {
		return err
	}
	home := r.sandboxHome(wt)
	common, private := sandboxGitDirs(r, home, wt)
	env := r.env.Environ()
	if env == nil {
		env = os.Environ()
	}
	policy, err := sandbox.Resolve(sandbox.Request{
		Mode:       *access,
		Worktree:   wt,
		GitCommon:  common,
		GitPrivate: private,
		Remotes:    gitRemotes(r.ctx(), wt, common, func() []string { return sandboxProjects(r, home) }),
		Harness:    spec,
		Home:       home,
		Bin:        bin,
		Getenv:     r.env.Getenv,
		Environ:    env,
	})
	if err != nil {
		return err
	}
	if *printOnly {
		return r.print(sandboxDoc(policy))
	}
	if *access == sandbox.Boxed {
		if p := sandbox.Prober(); !p.UnixSockets {
			return fmt.Errorf("%w: boxed is unavailable: %s", state.ErrInvalid, p.Reason)
		}
	}
	if err := sandbox.Apply(policy); err != nil {
		return fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	if err := closeOnExec(); err != nil {
		return fmt.Errorf("%w: close descriptors above 2: %v", state.ErrInvalid, err)
	}
	argv[0] = bin
	if r.env.Exec != nil {
		return r.env.Exec(argv[0], argv, policy.Environ)
	}
	return replaceProcess(argv, policy.Environ)
}

func (r *runner) sandboxTarget(attempt, worktree, harness string) (string, string, error) {
	if attempt != "" {
		st, err := r.store()
		if err != nil {
			return "", "", err
		}
		defer st.Close()
		id, err := parseID("a", attempt)
		if err != nil {
			return "", "", err
		}
		a, err := st.Attempt(r.ctx(), id)
		if err != nil {
			return "", "", err
		}
		if harness == "" {
			harness = a.Harness
		}
		return a.Worktree, harness, nil
	}
	dir := worktree
	if dir == "" {
		wd, err := r.getwd()
		if err != nil {
			return "", "", err
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	return abs, harness, nil
}

func (r *runner) sandboxHome(worktree string) string {
	if r.home != "" {
		return r.home
	}
	id := filepath.Base(filepath.Dir(worktree))
	if filepath.Base(filepath.Dir(filepath.Dir(worktree))) != "worktrees" {
		return ""
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return ""
	}
	home, err := fleet.Home(root, id)
	if err != nil {
		return ""
	}
	return home
}

func sandboxProjects(r *runner, home string) []string {
	if home == "" {
		return nil
	}
	st, err := state.Open(filepath.Join(home, "hand.db"), r.env.Now)
	if err != nil {
		return nil
	}
	defer st.Close()
	projects, err := st.Projects(r.ctx())
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Repo)
	}
	return paths
}

func sandboxGitDirs(r *runner, home, worktree string) (common, private string) {
	if home != "" {
		if st, err := state.Open(filepath.Join(home, "hand.db"), r.env.Now); err == nil {
			defer st.Close()
			if repo, ok := attemptRepo(r, st, worktree); ok {
				common = gitDirAt(repo)
				private = linkedAdminDir(common, worktree)
			}
		}
	}
	return common, private
}

func attemptRepo(r *runner, st *state.Store, worktree string) (string, bool) {
	a, err := st.AttemptByWorktree(r.ctx(), worktree)
	if err != nil {
		return "", false
	}
	t, err := st.Task(r.ctx(), a.TaskID)
	if err != nil {
		return "", false
	}
	p, err := st.Project(r.ctx(), t.Project)
	if err != nil {
		return "", false
	}
	return p.Repo, true
}

func linkedAdminDir(common, worktree string) string {
	if common == "" {
		return ""
	}
	wt, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return ""
	}
	dotGit := filepath.Join(wt, ".git")
	if fi, err := os.Lstat(dotGit); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	entries, err := filepath.Glob(filepath.Join(common, "worktrees", "*"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(filepath.Base(entry), filepath.Base(wt))
		if !ok || strings.Trim(suffix, "0123456789") != "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(entry, "gitdir"))
		if err != nil {
			continue
		}
		target := strings.TrimSpace(string(b))
		if !filepath.IsAbs(target) {
			target = filepath.Join(entry, target)
		}
		if filepath.Clean(target) == dotGit {
			return entry
		}
	}
	return ""
}

func gitDirAt(base string) string {
	path := filepath.Join(base, ".git")
	if fi, err := os.Lstat(path); err == nil && fi.IsDir() {
		return path
	}
	fi, err := os.Lstat(base)
	if err != nil || !fi.IsDir() {
		return ""
	}
	if head, err := os.Stat(filepath.Join(base, "HEAD")); err != nil || head.IsDir() {
		return ""
	}
	if objects, err := os.Stat(filepath.Join(base, "objects")); err != nil || !objects.IsDir() {
		return ""
	}
	return base
}

func gitRemotes(ctx context.Context, worktree, common string, projects func() []string) []string {
	if common == "" || within(realPath(common), realPath(worktree)) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--file", filepath.Join(common, "config"), "--get-regexp", `^remote\..*\.url$`)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		_, url, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if p, ok := localRemote(strings.TrimSpace(url), worktree, projects); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

func localRemote(url, worktree string, projects func() []string) (string, bool) {
	switch {
	case strings.HasPrefix(url, "file://"):
		url = strings.TrimPrefix(url, "file://")
	case strings.Contains(url, "://"):
		return "", false
	case !filepath.IsAbs(url) && strings.Contains(url, ":"):
		return "", false
	}
	url = strings.TrimSuffix(url, "/")
	if strings.HasPrefix(url, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		url = filepath.Join(home, url[2:])
	}
	if !filepath.IsAbs(url) {
		url = filepath.Join(worktree, url)
	}
	real, err := filepath.EvalSymlinks(url)
	if err != nil {
		return "", false
	}
	dir := gitDirAt(real)
	if dir == "" {
		return "", false
	}
	if home, err := os.UserHomeDir(); err == nil && within(real, realPath(home)) && !registered(projects(), real) {
		return "", false
	}
	return realPath(dir), true
}

func registered(projects []string, path string) bool {
	for _, p := range projects {
		if realPath(p) == path {
			return true
		}
	}
	return false
}

func realPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func sandboxDoc(p sandbox.Policy) *toon.Doc {
	var d toon.Doc
	d.Field("access", p.Mode)
	d.Field("network", "open")
	if p.Mode == sandbox.Open {
		d.Help("Nothing is confined. Give --access boxed to confine the command.")
		return &d
	}
	probe := sandbox.Probe()
	d.Field("backend", probe.Backend)
	d.Field("abi", fmt.Sprint(probe.ABI))
	d.Field("worktree", p.Worktree)
	d.Field("temp", p.Temp)
	rows := make([][]string, 0, len(p.Rules))
	for _, rule := range p.Rules {
		level := "ro"
		if rule.Write {
			level = "rw"
		}
		rows = append(rows, []string{rule.Path, level})
	}
	d.Rows("rules", []string{"path", "access"}, rows)
	d.List("dropped", p.Dropped)
	return &d
}

func cmdSandboxCheck(r *runner, args []string) error {
	if _, err := parse(flags("sandbox check"), args, 0); err != nil {
		return err
	}
	var d toon.Doc
	var c checks
	probe := sandbox.Probe()
	d.Field("backend", probe.Backend)
	if probe.ABI > 0 {
		d.Field("abi", fmt.Sprint(probe.ABI))
	}
	switch {
	case probe.Backend == "none":
		c.add("landlock", "fail", probe.Reason)
	case probe.UnixSockets:
		c.add("landlock", "ok", "")
	default:
		c.add("landlock", "fail", probe.Reason)
	}
	if probe.UnixSockets {
		c.add("unix sockets", "ok", "")
	} else {
		c.add("unix sockets", "fail", "boxed refuses to run here; run a kernel with Landlock ABI 9 or newer, or keep profiles open")
	}
	d.Rows("checks", []string{"check", "state", "fix"}, c.rows)
	d.Help(c.help...)
	if err := r.print(&d); err != nil {
		return err
	}
	if c.failed > 0 {
		return fmt.Errorf("sandbox: boxed cannot be enforced here; follow the fix lines")
	}
	return nil
}
