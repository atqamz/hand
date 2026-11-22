package cli

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const skillsRepo = "https://github.com/atqamz/skills.git"

var (
	skillRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	oddRef   = regexp.MustCompile(`(?i)^(refs|pull|merge-requests|changes|notes)/|\.\.`)

	skillGitTimeout  = 90 * time.Second
	skillFetchBudget = 3 * time.Minute
)

var githubRepo = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+?)(?:\.git)?/?$`)

var skillsCommands = map[string]handler{
	"list":     cmdSkillsList,
	"add":      cmdSkillsAdd,
	"update":   cmdSkillsUpdate,
	"remove":   cmdSkillsRemove,
	"outdated": cmdSkillsOutdated,
	"sync":     cmdSkillsSync,
	"propose":  cmdSkillsPropose,
}

func init() {
	commands["skills"] = func(r *runner, args []string) error { return sub(r, args, "skills", skillsCommands) }
}

type scopeFlags struct {
	user, fleet bool
	project     string
}

func (r *runner) scopeFlags(cmd string, args []string, most int, extra func(*flag.FlagSet)) (scopeFlags, []string, error) {
	set := flags("skills " + cmd)
	var f scopeFlags
	set.BoolVar(&f.user, "user", false, "act on the worker skills in ~/.agents/skills and its sibling folders")
	set.BoolVar(&f.fleet, "fleet", false, "act on the supervisor's skills in the fleet home")
	set.StringVar(&f.project, "project", "", "act on `NAME`, a project of this fleet, in its checkout's .agents/skills")
	if extra != nil {
		extra(set)
	}
	if err := set.Parse(args); err != nil {
		return f, nil, helpOr(set, err, usageError{fmt.Sprintf("skills %s: %v", cmd, err)})
	}
	usage := usageError{fmt.Sprintf("usage: hand skills %s [--project NAME|--fleet|--user]%s", cmd, map[string]string{"list": "", "add": " [--ref REF] NAME|PATH|URL", "update": " [NAME]", "remove": " [NAME]", "outdated": " [--all]", "sync": " [--yes NAME@COMMIT] [NAME]", "propose": " NAME"}[cmd])}
	if set.NArg() > most || (most == 1 && (cmd == "add" || cmd == "propose") && set.NArg() == 0) {
		return f, nil, usage
	}
	chosen := 0
	for _, on := range []bool{f.user, f.fleet, f.project != ""} {
		if on {
			chosen++
		}
	}
	if chosen > 1 {
		return f, nil, usage
	}
	r.scopeArg = f.arg()
	return f, set.Args(), nil
}

func (f scopeFlags) arg() string {
	switch {
	case f.user:
		return "--user "
	case f.fleet:
		return "--fleet "
	case f.project != "":
		return "--project " + f.project + " "
	}
	return ""
}

func (r *runner) resolveScope(f scopeFlags) (fleet.Skills, error) {
	switch {
	case f.user:
		home := r.env.Getenv("HOME")
		if home == "" {
			return fleet.Skills{}, fmt.Errorf("%w: HOME is not set", state.ErrInvalid)
		}
		return fleet.Skills{Base: home, Scope: "user"}, nil
	case f.fleet:
		if err := r.needHome(); err != nil {
			return fleet.Skills{}, err
		}
		return fleet.Skills{Base: r.home, Scope: "fleet"}, nil
	case f.project != "":
		return r.fleetProject(f.project)
	}
	return r.checkoutSkills()
}

func (r *runner) skillScope(cmd string, args []string, most int, extra func(*flag.FlagSet)) (fleet.Skills, []string, error) {
	f, rest, err := r.scopeFlags(cmd, args, most, extra)
	if err != nil {
		return fleet.Skills{}, nil, err
	}
	s, err := r.resolveScope(f)
	return s, rest, err
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

func localSource(base, source string) string {
	source = filepath.FromSlash(source)
	if filepath.IsAbs(source) {
		return source
	}
	return filepath.Join(base, source)
}

type cloned struct {
	repo, commit string
	err          error
}

type fetchCache struct {
	tmp    string
	ctx    context.Context
	cancel context.CancelFunc
	clones map[string]cloned
}

func (r *runner) closeFetches() {
	if r.fetches.cancel != nil {
		r.fetches.cancel()
	}
	if r.fetches.tmp != "" {
		_ = os.RemoveAll(r.fetches.tmp)
	}
	r.fetches = fetchCache{}
}

func (r *runner) clone(url, ref string) cloned {
	key := url + "\x00" + ref
	if c, ok := r.fetches.clones[key]; ok {
		return c
	}
	c := r.fetchClone(url, ref)
	if r.fetches.clones == nil {
		r.fetches.clones = map[string]cloned{}
	}
	r.fetches.clones[key] = c
	return c
}

func (r *runner) fetchClone(url, ref string) cloned {
	fc := &r.fetches
	if fc.tmp == "" {
		tmp, err := os.MkdirTemp("", "hand-skills-")
		if err != nil {
			return cloned{err: err}
		}
		fc.tmp = tmp
		fc.ctx, fc.cancel = context.WithTimeout(r.ctx(), skillFetchBudget)
	}
	dir, err := os.MkdirTemp(fc.tmp, "c")
	if err != nil {
		return cloned{err: err}
	}
	repo := filepath.Join(dir, "repo")
	for _, args := range [][]string{{"init", "--quiet", "repo"}, {"-C", "repo", "fetch", "--quiet", "--depth", "1", "--", url, ref}, {"-C", "repo", "checkout", "--quiet", "FETCH_HEAD"}} {
		if _, err := gitcfg.RunBounded(fc.ctx, dir, skillGitTimeout, args...); err != nil {
			return cloned{err: err}
		}
	}
	out, err := gitcfg.RunBounded(fc.ctx, repo, skillGitTimeout, "rev-parse", "HEAD")
	if err != nil {
		return cloned{err: err}
	}
	return cloned{repo: repo, commit: strings.TrimSpace(out)}
}

func (r *runner) fetchSkill(base string, e fleet.Managed) (dir, commit string, err error) {
	if e.Local() {
		src := localSource(base, e.Source)
		if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
			return "", "", fmt.Errorf("source missing: %s", src)
		}
		return src, "", nil
	}
	ref := cmp.Or(e.Ref, "HEAD")
	if !skillRef.MatchString(ref) || oddRef.MatchString(ref) {
		return "", "", fmt.Errorf("%w: %q is not a branch, tag or commit name", state.ErrInvalid, ref)
	}
	c := r.clone(e.URL(), ref)
	if c.err != nil {
		return "", "", c.err
	}
	dir, err = clonedSkillDir(c.repo, e.Path())
	return dir, c.commit, err
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
		if _, err := os.Stat(filepath.Join(localSource(s.Base, e.Source), "SKILL.md")); err != nil {
			return "source missing", nil
		}
	}
	return "ok", nil
}

type skillEntry struct {
	name, source, commit, ref, state, detail, latest, diff string
	managed, trusted                                       bool
}

func (r *runner) skillEntries(s fleet.Skills, withDiff bool) ([]skillEntry, error) {
	managed, err := s.Load()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for name := range managed {
		names[name] = true
	}
	entries, err := os.ReadDir(filepath.Join(s.Base, fleet.SkillRoots[1], "skills"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Dir(fleet.SkillRoots[1], e.Name()), "SKILL.md")); err == nil {
			names[e.Name()] = true
		}
	}
	out := []skillEntry{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		e, recorded := managed[name]
		switch {
		case recorded:
			source := e.URL()
			if p := e.Path(); p != "" {
				source += "#" + p
			}
			se := skillEntry{name: name, source: source, commit: e.Commit, ref: e.Ref, managed: true, trusted: trusted(s, e)}
			se.state, se.latest, se.detail, se.diff = r.probeDrift(s, name, e, withDiff)
			out = append(out, se)
		case fleet.Owned(s.Dir(fleet.SkillRoots[1], name)):
			out = append(out, skillEntry{name: name, source: "hand init"})
		default:
			out = append(out, skillEntry{name: name})
		}
	}
	return out, nil
}

func cmdSkillsList(r *runner, args []string) error {
	s, _, err := r.skillScope("list", args, 0, nil)
	if err != nil {
		return err
	}
	defer r.closeFetches()
	entries, err := r.skillEntries(s, false)
	if err != nil {
		return err
	}
	rows := [][]string{}
	for _, e := range entries {
		rows = append(rows, []string{e.name, e.source, e.commit, s.Scope, map[bool]string{true: "yes", false: "no"}[e.managed], e.ref, e.state, e.detail})
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "source", "commit", "scope", "managed", "ref", "state", "detail"}, rows)
	return r.print(&d)
}

func cmdSkillsAdd(r *runner, args []string) error {
	var ref string
	s, rest, err := r.skillScope("add", args, 1, func(f *flag.FlagSet) {
		f.StringVar(&ref, "ref", "", "track the branch, or pin the tag or commit, `REF`; a third-party source without one is pinned to its current commit")
	})
	if err != nil {
		return err
	}
	name, e, err := parseSkillSource(rest[0])
	if err != nil {
		return err
	}
	if ref != "" && e.Local() {
		return fmt.Errorf("%w: a local source has no ref", state.ErrInvalid)
	}
	e.Ref = ref
	if e.Ref == "" && !e.Local() && ownURL(e.URL()) {
		e.Ref = "main"
	}
	defer r.closeFetches()
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
	r.rec.rows, r.rec.done = [][]string{{name, "added", e.Commit}}, true
	d.Rows("skills", []string{"name", "state", "detail"}, r.rec.rows)
	return r.print(&d)
}

func (r *runner) fetchStaged(s fleet.Skills, name string, e fleet.Managed) (staged, sum, commit string, err error) {
	src, commit, err := r.fetchSkill(s.Base, e)
	if err != nil {
		return "", "", "", err
	}
	staged, sum, err = fleet.Stage(src, name)
	return staged, sum, commit, err
}

func (r *runner) installSkill(s fleet.Skills, name string, e *fleet.Managed) error {
	staged, sum, commit, err := r.fetchStaged(s, name, *e)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	e.Commit, e.ComputedHash = commit, sum
	if e.Ref == "" && !e.Local() {
		e.Ref = commit
	}
	return s.Install(name, staged, nil)
}

func (r *runner) apply(s fleet.Skills, managed map[string]fleet.Managed, name string, e fleet.Managed, staged, sum, commit string) error {
	if _, err := s.Backup(name, r.env.Now().UTC().Format("20060102T150405Z")); err != nil {
		return err
	}
	record := func() error {
		pending := e
		pending.Next = sum
		managed[name] = pending
		return s.Save(managed)
	}
	if err := s.Install(name, staged, record); err != nil {
		return err
	}
	e.Commit, e.ComputedHash, e.Next = commit, sum, ""
	managed[name] = e
	return nil
}

func (r *runner) eachSkill(cmd string, args []string, extra func(*flag.FlagSet), fn func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error), more func(*toon.Doc)) error {
	s, rest, err := r.skillScope(cmd, args, 1, extra)
	if err != nil {
		return err
	}
	for _, name := range rest {
		if !fleet.SkillName.MatchString(name) {
			return fmt.Errorf("%w: %q is not a skill name", state.ErrInvalid, name)
		}
	}
	defer r.closeFetches()
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
			st, detail = cmp.Or(st, "refused"), err.Error()
			refused++
		}
		rows = append(rows, []string{name, st, detail})
	}
	if len(names) > 0 {
		if err := s.Save(managed); err != nil {
			return err
		}
	}
	r.rec.rows, r.rec.refused, r.rec.done = rows, refused, true
	var d toon.Doc
	d.Rows("skills", []string{"name", "state", "detail"}, rows)
	if more != nil {
		more(&d)
	}
	if err := r.print(&d); err != nil {
		return err
	}
	if refused > 0 {
		return fmt.Errorf("%w: %d skill(s) not %s; see the detail column", state.ErrConflict, refused, map[string]string{"update": "updated", "remove": "removed", "sync": "synced"}[cmd])
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
	var goals []string
	return r.eachSkill("update", args, nil, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if !ok {
			return "", "", fmt.Errorf("%s has no recorded source; `hand skills add` records one", name)
		}
		if err := r.refuseDirty(s, name); err != nil {
			return "", "", err
		}
		kind, err := refuseUnlessClean(s, name, e, ok)
		if kind == "changed" || kind == "unmanaged" {
			if p, perr := r.compare(s, name, e); perr == nil {
				_ = os.RemoveAll(p.staged)
				if p.state == "conflict" {
					return "conflict", "", r.conflict(&goals, s, name, e, p)
				}
				if kind == "changed" {
					err = fmt.Errorf("%w: %s %s", state.ErrConflict, name, r.edited(name))
				}
			}
		}
		if err != nil {
			return "", "", err
		}
		staged, sum, commit, err := r.fetchStaged(s, name, e)
		if err != nil {
			return "", "", err
		}
		defer os.RemoveAll(staged)
		moved := sum != e.ComputedHash
		if !moved && kind == "" {
			return "current", e.Commit, nil
		}
		if err := r.apply(s, managed, name, e, staged, sum, commit); err != nil {
			return "", "", err
		}
		return map[bool]string{true: "updated", false: "restored"}[moved], commit, nil
	}, func(d *toon.Doc) {
		r.rec.goals = goals
		conflictHelp(d, goals)
	})
}

func cmdSkillsRemove(r *runner, args []string) error {
	return r.eachSkill("remove", args, nil, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
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
	}, nil)
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
