package cli

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var ownRepo = githubRepo.FindStringSubmatch(skillsRepo)[1]

func ownURL(url string) bool {
	m := githubRepo.FindStringSubmatch(url)
	return m != nil && m[1] == ownRepo
}

func trusted(s fleet.Skills, e fleet.Managed) bool {
	if e.Local() {
		return s.Scope != "project" && e.Source != "" && e.Subscribed == e.Source
	}
	return ownURL(e.URL()) && (e.Subscribed == ownRepo || ownURL(e.Subscribed))
}

type probed struct{ state, commit, staged, sum string }

func driftState(edited, behind bool) string {
	switch {
	case edited && behind:
		return "conflict"
	case edited:
		return "edited"
	case behind:
		return "behind"
	}
	return "current"
}

func (r *runner) compare(s fleet.Skills, name string, e fleet.Managed) (probed, error) {
	kind, _, err := s.Check(name, e)
	if err != nil {
		return probed{}, err
	}
	staged, sum, commit, err := r.fetchStaged(s, name, e)
	if err != nil {
		return probed{}, err
	}
	p := probed{commit: commit, staged: staged, sum: sum}
	p.state = driftState(kind == "changed" || kind == "unmanaged", sum != e.ComputedHash || kind == "missing")
	return p, nil
}

var (
	unsafeWord   = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	unsafeSource = regexp.MustCompile(`[^A-Za-z0-9._:/@+=-]`)
)

func addArgs(name string, e fleet.Managed) string {
	src := e.Source
	if !e.Local() {
		src = e.URL()
		if ownURL(src) && e.Path() == path.Join("skills", name) {
			src = name
		}
	}
	src = unsafeSource.ReplaceAllString(src, "?")
	if strings.HasPrefix(src, "-") {
		src = "?" + src[1:]
	}
	if e.Local() || e.Ref == "" {
		return src
	}
	return "--ref " + unsafeSource.ReplaceAllString(e.Ref, "?") + " " + src
}

func commitWord(commit string) string {
	return cmp.Or(unsafeWord.ReplaceAllString(short(commit), "?"), "its content")
}

func (r *runner) edited(name string) string {
	return fmt.Sprintf("changed here since the copy, the source did not move; nothing updated; `%s skills propose %s%s` sends the edit to its source", r.env.command(), r.scopeArg, name)
}

func (r *runner) conflict(goals *[]string, s fleet.Skills, name string, e fleet.Managed, p probed) error {
	hand, project := r.env.command(), "PROJECT"
	if f := strings.Fields(r.scopeArg); len(f) == 2 && f[0] == "--project" {
		project = unsafeWord.ReplaceAllString(f[1], "?")
	}
	goal := fmt.Sprintf("Reconcile the skill %[1]s of the %[2]s scope: its copy was edited here and its source moved from %[3]s to %[4]s. Save the edited folder outside the skills roots, delete %[1]s from every skills root of the scope, run %[5]s skills add %[6]s%[7]s, apply the saved edit to the new copy, then run %[5]s skills propose %[6]s%[1]s and stop at the pull request it prints; if the edit is no longer wanted, stop after the add.", name, s.Scope, commitWord(e.Commit), commitWord(p.commit), hand, r.scopeArg, addArgs(name, e))
	*goals = append(*goals, fmt.Sprintf("`%s task add --goal \"%s\" %s \"Reconcile skill %s\"`", hand, goal, project, name))
	return fmt.Errorf("%w: %s is a conflict: changed here and moved at its source (%s to %s); nothing updated; reconcile first, then `%s skills propose %s%s`; the help line has the task to hand a worker", state.ErrConflict, name, commitWord(e.Commit), commitWord(p.commit), hand, r.scopeArg, name)
}

func conflictHelp(d *toon.Doc, goals []string) {
	for _, g := range goals {
		d.Help("reconcile a conflict with a task: " + g)
	}
}

func short(commit string) string {
	return commit[:min(len(commit), 12)]
}

func (r *runner) driftRow(s fleet.Skills, name string, e fleet.Managed) (st, latest, detail string) {
	p, err := r.compare(s, name, e)
	if err != nil {
		return "error", "", err.Error()
	}
	_ = os.RemoveAll(p.staged)
	return p.state, p.commit, ""
}

func (r *runner) outdatedRows(s fleet.Skills, label string) ([][]string, error) {
	managed, err := s.Load()
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, name := range slices.Sorted(maps.Keys(managed)) {
		e := managed[name]
		source := e.URL()
		if p := e.Path(); p != "" {
			source += "#" + p
		}
		st, latest, detail := r.driftRow(s, name, e)
		rows = append(rows, []string{name, st, label, source, e.Ref, e.Commit, latest, detail})
	}
	return rows, nil
}

func cmdSkillsOutdated(r *runner, args []string) error {
	defer r.closeFetches()
	var all bool
	f, _, err := r.scopeFlags("outdated", args, 0, func(set *flag.FlagSet) {
		set.BoolVar(&all, "all", false, "check the fleet's skills and those of every project of this fleet")
	})
	if err != nil {
		return err
	}
	type scope struct {
		label string
		s     fleet.Skills
		err   error
	}
	var scopes []scope
	if all {
		if f != (scopeFlags{}) {
			return usageError{"usage: hand skills outdated [--all | --project NAME|--fleet|--user]"}
		}
		if err := r.needHome(); err != nil {
			return err
		}
		scopes = append(scopes, scope{label: "fleet", s: fleet.Skills{Base: r.home, Scope: "fleet"}})
		st, err := r.store()
		if err != nil {
			return err
		}
		defer st.Close()
		projects, err := st.Projects(r.ctx())
		if err != nil {
			return err
		}
		for _, p := range projects {
			sc := scope{label: "project/" + p.Name}
			sc.s, sc.err = r.projectSkills(p.Repo)
			scopes = append(scopes, sc)
		}
	} else {
		s, err := r.resolveScope(f)
		if err != nil {
			return err
		}
		scopes = append(scopes, scope{label: s.Scope, s: s})
	}
	rows := [][]string{}
	for _, sc := range scopes {
		got := sc.err
		if got == nil {
			var more [][]string
			more, got = r.outdatedRows(sc.s, sc.label)
			rows = append(rows, more...)
		}
		switch {
		case got == nil:
		case !all:
			return got
		default:
			rows = append(rows, []string{"", "error", sc.label, "", "", "", "", got.Error()})
		}
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "state", "scope", "source", "ref", "commit", "latest", "detail"}, rows)
	return r.print(&d)
}

func cmdSkillsSync(r *runner, args []string) error {
	var yes string
	set := func(f *flag.FlagSet) {
		f.StringVar(&yes, "yes", "", "apply the update of the third-party skill `NAME@COMMIT` (the whole commit id) whose diff you read")
	}
	diffs := map[string]string{}
	pending := false
	var goals []string
	return r.eachSkill("sync", args, set, func(s fleet.Skills, managed map[string]fleet.Managed, name string) (string, string, error) {
		e, ok := managed[name]
		if !ok {
			return "", "", fmt.Errorf("%s has no recorded source; `hand skills add` records one", name)
		}
		p, err := r.compare(s, name, e)
		if err != nil {
			return "", "", err
		}
		defer os.RemoveAll(p.staged)
		switch p.state {
		case "current":
			return "current", e.Commit, nil
		case "edited":
			return "edited", r.edited(name), nil
		case "conflict":
			return "conflict", "", r.conflict(&goals, s, name, e, p)
		}
		if !trusted(s, e) {
			id := cmp.Or(p.commit, p.sum)
			yesName, yesID, _ := strings.Cut(yes, "@")
			if yes != "" && (yesName == "" || yesID == "") {
				return "", "", fmt.Errorf("--yes takes the whole NAME@COMMIT of the diff you read: `hand skills sync --yes NAME@COMMIT`")
			}
			if name != yesName || yesID != id {
				if diffs[name], err = skillDiff(r, s, name, p.staged); err != nil {
					return "", "", err
				}
				pending = true
				if name == yesName {
					return "", "", fmt.Errorf("%w: %s moved since the diff you read: latest %s; read the diff above, then `hand skills sync --yes %s@%s`", state.ErrConflict, name, id, name, id)
				}
				return "behind", fmt.Sprintf("third-party, latest %s; read the diff, then `hand skills sync --yes %s@%s`", id, name, id), nil
			}
		}
		if err := r.refuseDirty(s, name); err != nil {
			return "", "", err
		}
		if err := r.apply(s, managed, name, e, p.staged, p.sum, p.commit); err != nil {
			return "", "", err
		}
		return "updated", short(e.Commit) + " -> " + short(p.commit), nil
	}, func(d *toon.Doc) {
		for _, name := range slices.Sorted(maps.Keys(diffs)) {
			d.Text("diff-"+name, diffs[name])
		}
		if pending {
			d.Help("a third-party update is never applied without a yes: read the diff, then `hand skills sync --yes NAME@COMMIT`")
		}
		conflictHelp(d, goals)
	})
}

func skillDiff(r *runner, s fleet.Skills, name, staged string) (string, error) {
	tmp, err := os.MkdirTemp("", "hand-skills-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.Mkdir(filepath.Join(tmp, "old"), 0o755); err != nil {
		return "", err
	}
	for _, root := range s.Roots() {
		if _, err := os.Lstat(s.Dir(root, name)); err == nil {
			if err := fleet.Copy(s.Dir(root, name), filepath.Join(tmp, "old")); err != nil {
				return "", err
			}
			break
		}
	}
	if err := fleet.Copy(staged, filepath.Join(tmp, "new")); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(r.ctx(), "git", "-c", "core.quotepath=false", "diff", "--no-index", "--no-color", "--no-ext-diff", "--no-prefix", "--", "old", "new")
	cmd.Dir = tmp
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: diff of %s: %v", state.ErrInvalid, name, err)
	}
	return string(out), nil
}
