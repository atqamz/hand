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

const skillsRepo = "https://github.com/atqamz/hand-skills.git"

var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
	if err := set.Parse(args); err != nil {
		return fleet.Skills{}, nil, helpOr(set, err, usageError{fmt.Sprintf("skills %s: %v", cmd, err)})
	}
	if set.NArg() > most || (most == 1 && cmd == "add" && set.NArg() == 0) {
		return fleet.Skills{}, nil, usageError{fmt.Sprintf("usage: hand skills %s [--user]%s", cmd, map[string]string{"list": "", "add": " NAME|PATH|URL", "update": " [NAME]", "remove": " [NAME]"}[cmd])}
	}
	if *user {
		home := r.env.Getenv("HOME")
		if home == "" {
			return fleet.Skills{}, nil, fmt.Errorf("%w: HOME is not set", state.ErrInvalid)
		}
		return fleet.Skills{Base: home, Scope: "user"}, set.Args(), nil
	}
	if err := r.needHome(); err != nil {
		return fleet.Skills{}, nil, err
	}
	return fleet.Skills{Base: r.home, Scope: "fleet"}, set.Args(), nil
}

func isURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

func parseSkillSource(arg string) (string, fleet.Managed, error) {
	var name string
	var e fleet.Managed
	switch {
	case isURL(arg):
		trimmed := strings.TrimRight(arg, "/")
		name, e.Source = trimmed[strings.LastIndexAny(trimmed, "/:")+1:], arg
	case strings.ContainsAny(arg, `/\`) || strings.HasPrefix(arg, "."):
		abs, err := filepath.Abs(arg)
		if err != nil {
			return "", e, err
		}
		name, e.Source = filepath.Base(abs), abs
	default:
		name, e.Source, e.Path = arg, skillsRepo, path.Join("skills", arg)
	}
	name = strings.TrimSuffix(name, ".git")
	if !skillName.MatchString(name) {
		return "", e, fmt.Errorf("%w: %q is not a skill name", state.ErrInvalid, name)
	}
	return name, e, nil
}

func fetchSkill(ctx context.Context, e fleet.Managed) (dir, commit string, cleanup func(), err error) {
	cleanup = func() {}
	if !isURL(e.Source) {
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
	if _, err := gitcfg.Run(ctx, tmp, "clone", "--quiet", "--depth", "1", "--", e.Source, "repo"); err != nil {
		return "", "", cleanup, err
	}
	out, err := gitcfg.Run(ctx, filepath.Join(tmp, "repo"), "rev-parse", "HEAD")
	if err != nil {
		return "", "", cleanup, err
	}
	return filepath.Join(tmp, "repo", filepath.FromSlash(e.Path)), strings.TrimSpace(out), cleanup, nil
}

func skillState(s fleet.Skills, name string, e fleet.Managed) (string, error) {
	kind, _, err := s.Check(name, e)
	switch {
	case err != nil:
		return "", err
	case kind != "":
		return "drifted", nil
	case !isURL(e.Source):
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
		if _, err := os.Stat(filepath.Join(s.Dir(fleet.SkillRoots[1], e.Name()), "SKILL.md")); err == nil {
			names[e.Name()] = true
		}
	}
	rows := [][]string{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		e, recorded := managed[name]
		dir := s.Dir(fleet.SkillRoots[1], name)
		have, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		switch {
		case fleet.Owned(dir) || recorded:
			source := e.Source
			if e.Path != "" {
				source += "#" + e.Path
			}
			rows = append(rows, []string{name, source, e.Commit, s.Scope, "yes"})
		case strings.Contains(string(have), "\n  managed-by: hand\n"):
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
	e.Scope = s.Scope
	managed, err := s.Load()
	if err != nil {
		return err
	}
	for _, root := range fleet.SkillRoots {
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
	staged, sum, err := fleet.Stage(src)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	e.Commit, e.Sum = commit, sum
	return s.Install(name, staged)
}

func (r *runner) eachSkill(cmd string, args []string, fn func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error)) error {
	s, rest, err := r.skillScope(cmd, args, 1)
	if err != nil {
		return err
	}
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
	}
	return kind, nil
}

func cmdSkillsUpdate(r *runner, args []string) error {
	return r.eachSkill("update", args, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if !ok {
			return "", "", fmt.Errorf("%s has no recorded source; `hand skills add` records one", name)
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
		staged, sum, err := fleet.Stage(src)
		if err != nil {
			return "", "", err
		}
		defer os.RemoveAll(staged)
		moved := sum != e.Sum
		if isURL(e.Source) {
			moved = commit != e.Commit
		}
		if !moved && kind == "" {
			return "current", e.Commit, nil
		}
		if err := s.Install(name, staged); err != nil {
			return "", "", err
		}
		e.Commit, e.Sum = commit, sum
		managed[name] = e
		return map[bool]string{true: "updated", false: "restored"}[moved], commit, nil
	})
}

func cmdSkillsRemove(r *runner, args []string) error {
	return r.eachSkill("remove", args, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if _, err := refuseUnlessClean(s, name, e, ok); err != nil {
			return "", "", err
		}
		if err := s.Remove(name); err != nil {
			return "", "", err
		}
		delete(managed, name)
		return "removed", "", nil
	})
}

func (r *runner) doctorSkills(c *checks) {
	scopes := []fleet.Skills{{Base: r.home, Scope: "fleet"}}
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
