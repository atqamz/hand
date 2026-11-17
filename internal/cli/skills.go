package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const skillsRepo = "https://github.com/atqamz/skills.git"

var githubRepo = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+?)(?:\.git)?/?$`)

var skillsCommands = map[string]handler{
	"list":   cmdSkillsList,
	"add":    cmdSkillsAdd,
	"update": cmdSkillsUpdate,
	"remove": cmdSkillsRemove,
}

func init() {
	commands["skills"] = func(r *runner, args []string) error { return sub(r, args, "skills", skillsCommands) }
}

func (r *runner) skillScope(cmd string, args []string, most int) (fleet.Skills, []string, error) {
	set := flags("skills " + cmd)
	user := set.Bool("user", false, "act on the worker skills in ~/.agents/skills and its sibling folders")
	fleetHome := set.Bool("fleet", false, "act on the supervisor's skills in the fleet home")
	project := set.String("project", "", "act on `NAME`, a project of this fleet, in its checkout's .agents/skills")
	if err := set.Parse(args); err != nil {
		return fleet.Skills{}, nil, helpOr(set, err, usageError{fmt.Sprintf("skills %s: %v", cmd, err)})
	}
	usage := usageError{fmt.Sprintf("usage: hand skills %s [--project NAME|--fleet|--user]%s", cmd, map[string]string{"list": "", "add": " NAME|PATH|URL", "update": " [NAME]", "remove": " [NAME]"}[cmd])}
	if set.NArg() > most || (most == 1 && cmd == "add" && set.NArg() == 0) {
		return fleet.Skills{}, nil, usage
	}
	chosen := 0
	for _, on := range []bool{*user, *fleetHome, *project != ""} {
		if on {
			chosen++
		}
	}
	if chosen > 1 {
		return fleet.Skills{}, nil, usage
	}
	var s fleet.Skills
	var err error
	switch {
	case *user:
		home := r.env.Getenv("HOME")
		if home == "" {
			return fleet.Skills{}, nil, fmt.Errorf("%w: HOME is not set", state.ErrInvalid)
		}
		s = fleet.Skills{Base: home, Scope: "user"}
	case *fleetHome:
		if err = r.needHome(); err == nil {
			s = fleet.Skills{Base: r.home, Scope: "fleet"}
		}
	case *project != "":
		s, err = r.fleetProject(*project)
	default:
		s, err = r.checkoutSkills()
	}
	return s, set.Args(), err
}

func (r *runner) fleetProject(name string) (fleet.Skills, error) {
	st, err := r.store()
	if err != nil {
		return fleet.Skills{}, err
	}
	defer st.Close()
	p, err := st.Project(r.ctx(), name)
	if err != nil {
		return fleet.Skills{}, err
	}
	return r.projectSkills(p.Repo)
}

func (r *runner) projectSkills(base string) (fleet.Skills, error) {
	gitDir, err := gitcfg.Run(r.ctx(), base, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fleet.Skills{}, fmt.Errorf("%w: %s is not a git checkout: %v", state.ErrInvalid, base, err)
	}
	return fleet.Skills{Base: base, Scope: "project", Meta: filepath.Join(filepath.FromSlash(strings.TrimSpace(gitDir)), "hand-skills")}, nil
}

func (r *runner) checkoutSkills() (fleet.Skills, error) {
	wd, err := r.getwd()
	if err != nil {
		return fleet.Skills{}, err
	}
	if out, err := gitcfg.Toplevel(r.ctx(), wd); err == nil {
		top := filepath.FromSlash(strings.TrimSpace(out))
		if rel, err := filepath.Rel(top, r.home); r.home == "" || err != nil || strings.HasPrefix(rel, "..") {
			return r.projectSkills(top)
		}
	}
	if r.needHome() != nil {
		return fleet.Skills{}, fmt.Errorf("%w: %s is neither a project checkout nor a fleet home; cd into a checkout, or pass --user", state.ErrInvalid, wd)
	}
	st, err := r.store()
	if err != nil {
		return fleet.Skills{}, err
	}
	defer st.Close()
	projects, err := st.Projects(r.ctx())
	if err != nil {
		return fleet.Skills{}, err
	}
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	return fleet.Skills{}, fmt.Errorf("%w: the fleet home is not a project checkout; pass --project NAME (%s), --fleet or --user", state.ErrInvalid, strings.Join(names, ", "))
}

func (r *runner) refuseDirty(s fleet.Skills, name string) error {
	if s.Scope != "project" {
		return nil
	}
	rel := path.Join(fleet.SkillRoots[1], "skills", name)
	out, err := gitcfg.Run(r.ctx(), s.Base, "status", "--porcelain", "--", rel)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("%w: %s has uncommitted changes in git; commit or discard them first", state.ErrConflict, rel)
	}
	return nil
}

func isURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

func remoteEntry(url, skill string) fleet.LockEntry {
	e := fleet.LockEntry{Source: url, SourceType: "git"}
	if m := githubRepo.FindStringSubmatch(url); m != nil {
		e.Source, e.SourceURL, e.SourceType = m[1], url, "github"
	}
	if skill != "" {
		e.SkillPath = path.Join(skill, "SKILL.md")
	}
	return e
}

func parseSkillSource(arg string) (string, fleet.Managed, error) {
	var name string
	var e fleet.Managed
	switch {
	case isURL(arg):
		trimmed := strings.TrimRight(arg, "/")
		name = trimmed[strings.LastIndexAny(trimmed, "/:")+1:]
		e.LockEntry = remoteEntry(arg, "")
	case strings.ContainsAny(arg, `/\`) || strings.HasPrefix(arg, "."):
		abs, err := filepath.Abs(arg)
		if err != nil {
			return "", e, err
		}
		name, e.LockEntry = filepath.Base(abs), fleet.LockEntry{Source: abs, SourceType: "local"}
	default:
		name, e.LockEntry = arg, remoteEntry(skillsRepo, path.Join("skills", arg))
	}
	name = strings.TrimSuffix(name, ".git")
	if !fleet.SkillName.MatchString(name) {
		return "", e, fmt.Errorf("%w: %q is not a skill name", state.ErrInvalid, name)
	}
	return name, e, nil
}

func fetchSkill(ctx context.Context, e fleet.Managed) (dir, commit string, cleanup func(), err error) {
	cleanup = func() {}
	if e.Local() {
		if _, err := os.Stat(filepath.Join(e.Source, "SKILL.md")); err != nil {
			return "", "", cleanup, fmt.Errorf("source missing: %s", e.Source)
		}
		return e.Source, "", cleanup, nil
	}
	tmp, err := os.MkdirTemp("", "hand-skills-")
	if err != nil {
		return "", "", cleanup, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	if _, err := gitcfg.Run(ctx, tmp, "clone", "--quiet", "--depth", "1", "--", e.URL(), "repo"); err != nil {
		return "", "", cleanup, err
	}
	out, err := gitcfg.Run(ctx, filepath.Join(tmp, "repo"), "rev-parse", "HEAD")
	if err != nil {
		return "", "", cleanup, err
	}
	dir, err = clonedSkillDir(filepath.Join(tmp, "repo"), e.Path())
	return dir, strings.TrimSpace(out), cleanup, err
}

func clonedSkillDir(clone, skill string) (string, error) {
	rel := filepath.FromSlash(skill)
	if rel == "" {
		return clone, nil
	}
	root, err := filepath.EvalSymlinks(clone)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("%w: skill path %q: %v", state.ErrInvalid, skill, err)
	}
	if inside, err := filepath.Rel(root, dir); !filepath.IsLocal(rel) || err != nil || !filepath.IsLocal(inside) && inside != "." {
		return "", fmt.Errorf("%w: skill path %q leaves the repository", state.ErrInvalid, skill)
	}
	return dir, nil
}

func skillState(s fleet.Skills, name string, e fleet.Managed) (string, error) {
	kind, _, err := s.Check(name, e)
	switch {
	case err != nil:
		return "", err
	case kind != "":
		return "drifted", nil
	case e.Local():
		if _, err := os.Stat(filepath.Join(e.Source, "SKILL.md")); err != nil {
			return "source missing", nil
		}
	}
	return "ok", nil
}

func cmdSkillsList(r *runner, args []string) error {
	s, _, err := r.skillScope("list", args, 0)
	if err != nil {
		return err
	}
	managed, err := s.Load()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for name := range managed {
		names[name] = true
	}
	entries, err := os.ReadDir(filepath.Join(s.Base, fleet.SkillRoots[1], "skills"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Dir(fleet.SkillRoots[1], e.Name()), "SKILL.md")); err == nil {
			names[e.Name()] = true
		}
	}
	rows := [][]string{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		e, recorded := managed[name]
		switch {
		case recorded:
			source := e.URL()
			if p := e.Path(); p != "" {
				source += "#" + p
			}
			rows = append(rows, []string{name, source, e.Commit, s.Scope, "yes"})
		case fleet.Owned(s.Dir(fleet.SkillRoots[1], name)):
			rows = append(rows, []string{name, "hand init", "", s.Scope, "no"})
		default:
			rows = append(rows, []string{name, "", "", s.Scope, "no"})
		}
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "source", "commit", "scope", "managed"}, rows)
	return r.print(&d)
}

func cmdSkillsAdd(r *runner, args []string) error {
	s, rest, err := r.skillScope("add", args, 1)
	if err != nil {
		return err
	}
	name, e, err := parseSkillSource(rest[0])
	if err != nil {
		return err
	}
	if err := r.refuseDirty(s, name); err != nil {
		return err
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	managed, err := s.Load()
	if err != nil {
		return err
	}
	for _, root := range s.Roots() {
		if _, err := os.Lstat(s.Dir(root, name)); err == nil {
			return fmt.Errorf("%w: %s already exists; `hand skills update %s` refreshes a skill Hand manages", state.ErrConflict, s.Dir(root, name), name)
		}
	}
	if err := r.installSkill(s, name, &e); err != nil {
		_ = s.Remove(name)
		return err
	}
	managed[name] = e
	if err := s.Save(managed); err != nil {
		_ = s.Remove(name)
		return err
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "state", "detail"}, [][]string{{name, "added", e.Commit}})
	return r.print(&d)
}

func (r *runner) installSkill(s fleet.Skills, name string, e *fleet.Managed) error {
	src, commit, cleanup, err := fetchSkill(r.ctx(), *e)
	defer cleanup()
	if err != nil {
		return err
	}
	staged, sum, err := fleet.Stage(src, name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	e.Commit, e.ComputedHash = commit, sum
	return s.Install(name, staged, nil)
}

func (r *runner) eachSkill(cmd string, args []string, fn func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error)) error {
	s, rest, err := r.skillScope(cmd, args, 1)
	if err != nil {
		return err
	}
	for _, name := range rest {
		if !fleet.SkillName.MatchString(name) {
			return fmt.Errorf("%w: %q is not a skill name", state.ErrInvalid, name)
		}
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	managed, err := s.Load()
	if err != nil {
		return err
	}
	names := rest
	if len(names) == 0 {
		names = slices.Sorted(maps.Keys(managed))
	}
	rows := make([][]string, 0, len(names))
	refused := 0
	for _, name := range names {
		st, detail, err := fn(s, managed, name)
		if err != nil {
			st, detail = "refused", err.Error()
			refused++
		}
		rows = append(rows, []string{name, st, detail})
	}
	if len(names) > 0 {
		if err := s.Save(managed); err != nil {
			return err
		}
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "state", "detail"}, rows)
	if err := r.print(&d); err != nil {
		return err
	}
	if refused > 0 {
		return fmt.Errorf("%w: %d skill(s) not %s; see the detail column", state.ErrConflict, refused, map[string]string{"update": "updated", "remove": "removed"}[cmd])
	}
	return nil
}

func refuseUnlessClean(s fleet.Skills, name string, e fleet.Managed, ok bool) (string, error) {
	kind, root, err := s.Check(name, e)
	if err != nil {
		return "", err
	}
	switch {
	case kind == "unmanaged":
		return kind, fmt.Errorf("%s in %s is not marked as managed by hand skills; Hand leaves it alone", name, root)
	case kind == "changed":
		return kind, fmt.Errorf("%s changed locally in %s since the copy; Hand does not overwrite or delete it; move your edits away or delete its folders by hand", name, root)
	case !ok && kind == "missing":
		return kind, fmt.Errorf("%s is not a skill Hand manages in this scope", name)
	case !ok:
		return kind, fmt.Errorf("%s is marked as managed by hand skills but has no recorded copy in this scope; Hand does not delete a folder it cannot compare; delete its folders by hand", name)
	}
	return kind, nil
}

func cmdSkillsUpdate(r *runner, args []string) error {
	return r.eachSkill("update", args, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if !ok {
			return "", "", fmt.Errorf("%s has no recorded source; `hand skills add` records one", name)
		}
		if err := r.refuseDirty(s, name); err != nil {
			return "", "", err
		}
		kind, err := refuseUnlessClean(s, name, e, ok)
		if err != nil {
			return "", "", err
		}
		src, commit, cleanup, err := fetchSkill(r.ctx(), e)
		defer cleanup()
		if err != nil {
			return "", "", err
		}
		staged, sum, err := fleet.Stage(src, name)
		if err != nil {
			return "", "", err
		}
		defer os.RemoveAll(staged)
		moved := sum != e.ComputedHash
		if !e.Local() {
			moved = commit != e.Commit
		}
		if !moved && kind == "" {
			return "current", e.Commit, nil
		}
		record := func() error {
			pending := e
			pending.Next = sum
			managed[name] = pending
			return s.Save(managed)
		}
		if err := s.Install(name, staged, record); err != nil {
			return "", "", err
		}
		e.Commit, e.ComputedHash, e.Next = commit, sum, ""
		managed[name] = e
		return map[bool]string{true: "updated", false: "restored"}[moved], commit, nil
	})
}

func cmdSkillsRemove(r *runner, args []string) error {
	return r.eachSkill("remove", args, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if err := r.refuseDirty(s, name); err != nil {
			return "", "", err
		}
		if _, err := refuseUnlessClean(s, name, e, ok); err != nil {
			return "", "", err
		}
		if err := s.Remove(name); err != nil {
			return "", "", err
		}
		delete(managed, name)
		if err := s.Forget(name); err != nil {
			return "", "", err
		}
		return "removed", "", nil
	})
}

func (r *runner) doctorSkills(c *checks) {
	var scopes []fleet.Skills
	if r.home != "" {
		scopes = append(scopes, fleet.Skills{Base: r.home, Scope: "fleet"})
	}
	if home := r.env.Getenv("HOME"); home != "" {
		scopes = append(scopes, fleet.Skills{Base: home, Scope: "user"})
	}
	var bad []string
	for _, s := range scopes {
		managed, err := s.Load()
		if err != nil {
			bad = append(bad, s.Scope+": "+err.Error())
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(managed)) {
			st, err := skillState(s, name, managed[name])
			if err != nil {
				st = err.Error()
			}
			if st != "ok" {
				bad = append(bad, s.Scope+" "+name+" "+st)
			}
		}
	}
	if len(bad) == 0 {
		c.add("skills", "ok", "")
		return
	}
	c.add("skills", "warn", strings.Join(bad, "; ")+"; `hand skills update` refreshes a drifted skill, and a skill whose source is missing is removed or added again")
}
