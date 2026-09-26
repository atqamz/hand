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

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/memory"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	commands["init"] = cmdInit
}

func (r *runner) dbPath() string { return filepath.Join(r.home, "hand.db") }

func resolveHome(env Env, flagHome string, getwd func() (string, error)) (string, error) {
	if flagHome != "" {
		return canonical(flagHome)
	}
	wd, err := getwd()
	if err != nil {
		return "", err
	}
	found, err := findHome(wd)
	if err != nil {
		return "", err
	}
	if env.Getenv("HAND_HOME") == "" {
		return found, nil
	}
	home, err := canonical(env.Getenv("HAND_HOME"))
	if err != nil {
		return "", err
	}
	if found != "" && found != home {
		return "", fmt.Errorf("%w: HAND_HOME is %s but the working directory is inside %s; unset HAND_HOME to use %s, or run from outside it", state.ErrConflict, home, found, found)
	}
	return home, nil
}

func canonical(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}

func findHome(dir string) (string, error) {
	dir, err := canonical(dir)
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "hand.db"))
		switch {
		case err == nil && info.Mode().IsRegular():
			return dir, nil
		case err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR):
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func (r *runner) needHome() error {
	if r.home == "" {
		return fmt.Errorf("%w: not inside a fleet home; cd into one, pass --home DIR, or run `hand init` to make this folder one", state.ErrNotFound)
	}
	if _, err := os.Stat(r.dbPath()); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: no hand home at %s; run `hand init`", state.ErrNotFound, r.home)
	} else if err != nil {
		return err
	}
	return nil
}

func (r *runner) store() (*state.Store, error) {
	if err := r.needHome(); err != nil {
		return nil, err
	}
	st, err := state.Open(r.dbPath(), r.env.Now)
	if err != nil {
		return nil, err
	}
	if err := r.claim(st); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func (r *runner) claim(st *state.Store) error {
	f, err := st.Fleet(r.ctx())
	if err != nil {
		return err
	}
	if r.root == "" {
		if r.root, err = fleet.Root(r.env.Getenv); err != nil {
			return err
		}
	}
	if err := fleet.Check(r.root, f.ID, r.home); err != nil {
		return err
	}
	r.fleet = f
	return nil
}

var errHomeMoved = errors.New("this fleet's home moved or was copied")

func (r *runner) stillHome() error {
	if _, err := os.Stat(r.dbPath()); err != nil {
		return fmt.Errorf("%w: %w: %w", state.ErrConflict, errHomeMoved, err)
	}
	if err := fleet.Check(r.root, r.fleet.ID, r.home); err != nil {
		return fmt.Errorf("%w: %w", errHomeMoved, err)
	}
	return nil
}

func cmdInit(r *runner, args []string) error {
	set := flags("init")
	name := set.String("name", "", "fleet name people see; default: the folder name")
	if err := set.Parse(args); err != nil {
		return usageError{fmt.Sprintf("init: %v", err)}
	}
	if set.NArg() > 1 {
		return usageError{fmt.Sprintf("init: want at most one folder, got %d", set.NArg())}
	}
	if *name != "" {
		if err := state.ValidFleetName(*name); err != nil {
			return err
		}
	}
	dir := r.home
	if set.NArg() == 1 {
		dir = set.Arg(0)
	}
	if dir == "" {
		wd, err := r.getwd()
		if err != nil {
			return err
		}
		dir = wd
	}
	dir, err := canonical(dir)
	if err != nil {
		return err
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	if err := refuseNesting(dir, root); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if r.home, err = filepath.EvalSymlinks(dir); err != nil {
		return err
	}
	ctx := r.ctx()
	_, statErr := os.Stat(r.dbPath())
	created := errors.Is(statErr, fs.ErrNotExist)
	var st *state.Store
	var f state.Fleet
	have := false
	if !created {
		if st, err = state.Open(r.dbPath(), r.env.Now); err != nil {
			return err
		}
		defer st.Close()
		switch f, err = st.Fleet(ctx); {
		case err == nil:
			have = true
		case !errors.Is(err, state.ErrNotFound):
			return err
		}
	}
	movedFrom := ""
	if have {
		if movedFrom, err = fleet.Register(root, f.ID, r.home); err != nil {
			return err
		}
	}
	n := *name
	if !have && n == "" {
		n = filepath.Base(r.home)
		if err := state.ValidFleetName(n); err != nil {
			return fmt.Errorf("%w; pass --name", err)
		}
	}
	if err := fleet.Install(r.home); err != nil {
		return err
	}
	if err := memory.Init(r.home); err != nil {
		return err
	}
	if _, err := harness.WriteStarterPolicy(r.home); err != nil {
		return err
	}
	if st == nil {
		if st, err = state.Open(r.dbPath(), r.env.Now); err != nil {
			return err
		}
		defer st.Close()
	}
	switch {
	case !have:
		if f, err = st.CreateFleet(ctx, n); err != nil {
			return err
		}
		if movedFrom, err = fleet.Register(root, f.ID, r.home); err != nil {
			return err
		}
	case n != "" && n != f.Name:
		if f, err = st.RenameFleet(ctx, n); err != nil {
			return err
		}
	}
	if err := repairWorktrees(ctx, st); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("home", r.home)
	d.Field("fleet", f.Name)
	d.Field("id", f.ID)
	d.Bool("created", created)
	if movedFrom != "" {
		d.Field("moved_from", movedFrom)
	}
	d.Field("routing", filepath.Join(r.home, harness.PolicyFile))
	help := []string{"Write your durable preferences in " + filepath.Join(r.home, "memory", memory.OperatorFile),
		"Register a repository: `hand project add NAME ABSOLUTE_REPO_PATH`"}
	if movedFrom != "" {
		help = append(help, "This home moved; regenerate its systemd units with `hand unit watch` and `hand unit board`")
	}
	d.Help(help...)
	return r.print(&d)
}

func refuseNesting(home, root string) error {
	if within(root, home) {
		return fmt.Errorf("%w: %s holds Hand's shared folder %s; make the fleet in a folder of its own", state.ErrConflict, home, root)
	}
	if within(home, root) {
		return fmt.Errorf("%w: %s is inside Hand's shared folder %s; make the fleet somewhere else", state.ErrConflict, home, root)
	}
	outer, err := findHome(filepath.Dir(home))
	if err != nil {
		return err
	}
	if outer != "" {
		return fmt.Errorf("%w: %s is inside the fleet at %s; make the fleet somewhere else", state.ErrConflict, home, outer)
	}
	return nil
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func repairWorktrees(ctx context.Context, st *state.Store) error {
	as, err := st.UncleanedAttempts(ctx)
	if err != nil {
		return err
	}
	var repos []string
	byRepo := map[string][]string{}
	for _, a := range as {
		if _, err := os.Stat(a.Worktree); err != nil {
			continue
		}
		t, err := st.Task(ctx, a.TaskID)
		if err != nil {
			return err
		}
		p, err := st.Project(ctx, t.Project)
		if err != nil {
			return err
		}
		if _, err := os.Stat(p.Repo); err != nil {
			continue
		}
		if _, seen := byRepo[p.Repo]; !seen {
			repos = append(repos, p.Repo)
		}
		byRepo[p.Repo] = append(byRepo[p.Repo], a.Worktree)
	}
	for _, repo := range repos {
		if _, err := git(ctx, repo, append([]string{"worktree", "repair"}, byRepo[repo]...)...); err != nil {
			return err
		}
	}
	return nil
}
