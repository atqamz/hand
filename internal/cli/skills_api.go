package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

const skillsDiffLimit = 64 << 10

var urlLogin = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]*@`)

func clean(s string) string {
	return board.Scrub(urlLogin.ReplaceAllString(s, "${1}"))
}

type skillsRec struct {
	rows    [][]string
	diffs   map[string]string
	goals   []string
	refused int
	done    bool
}

func capDiff(d string) (string, bool) {
	if len(d) <= skillsDiffLimit {
		return d, false
	}
	return d[:strings.LastIndex(d[:skillsDiffLimit], "\n")+1], true
}

func apiRunner(env Env, ctx context.Context, home, generation string, args ...string) *runner {
	env.Stdout, env.Stderr, env.Context, env.Board = io.Discard, io.Discard, ctx, true
	env = withGeneration(env, generation)
	r := &runner{env: env, home: home, generation: env.Getenv("HAND_GENERATION")}
	r.caller = callerName(env.command(), args)
	return r
}

func skillsRead(env Env, home, generation string, st *state.Store) func(context.Context, board.SkillsQuery) (board.APISkills, error) {
	return func(ctx context.Context, q board.SkillsQuery) (board.APISkills, error) {
		r := apiRunner(env, ctx, home, generation, "skills", "list")
		defer r.closeFetches()
		out := board.APISkills{Scopes: []board.APISkillScope{}}
		add := func(scope, project string, s fleet.Skills, err error) {
			sc := board.APISkillScope{Scope: scope, Project: project, Skills: []board.APISkill{}}
			var entries []skillEntry
			if err == nil {
				entries, err = r.skillEntries(s, true)
			}
			if err != nil {
				sc.Error = clean(err.Error())
			}
			for _, e := range entries {
				diff, cut := capDiff(e.diff)
				sc.Skills = append(sc.Skills, board.APISkill{
					Name: e.name, Source: clean(e.source), Ref: e.ref, Commit: e.commit, Latest: e.latest,
					Managed: e.managed, Trusted: e.trusted, State: e.state, Detail: clean(e.detail), Diff: diff, DiffCut: cut,
				})
			}
			out.Scopes = append(out.Scopes, sc)
		}
		if q.Scope == "" || q.Scope == "fleet" {
			add("fleet", "", fleet.Skills{Base: home, Scope: "fleet"}, nil)
		}
		if q.Scope == "" || q.Scope == "project" {
			projects, err := st.Projects(ctx)
			if err != nil {
				return board.APISkills{}, err
			}
			if q.Project != "" {
				p, err := st.Project(ctx, q.Project)
				if err != nil {
					return board.APISkills{}, err
				}
				projects = []state.Project{p}
			}
			for _, p := range projects {
				s, err := r.projectSkills(p.Repo)
				add("project", p.Name, s, err)
			}
		}
		if q.Scope == "" || q.Scope == "user" {
			s, err := r.resolveScope(scopeFlags{user: true})
			add("user", "", s, err)
		}
		return out, nil
	}
}

func skillsAct(env Env, home, generation string) func(context.Context, board.SkillsAction) (board.APISkillsRun, error) {
	return func(ctx context.Context, a board.SkillsAction) (board.APISkillsRun, error) {
		r := apiRunner(env, ctx, home, generation, "skills", a.Verb)
		for _, v := range []string{a.Project, a.Name, a.Yes} {
			if strings.HasPrefix(v, "-") {
				return board.APISkillsRun{}, fmt.Errorf("%w: %q starts with a dash", state.ErrInvalid, v)
			}
		}
		args := []string{a.Verb}
		switch a.Scope {
		case "fleet", "user":
			args = append(args, "--"+a.Scope)
		case "project":
			args = append(args, "--project="+a.Project)
		}
		if a.Yes != "" {
			args = append(args, "--yes="+a.Yes)
		}
		if a.Name != "" {
			args = append(args, "--", a.Name)
		}
		err := commands["skills"](r, args)
		var usage usageError
		if errors.As(err, &usage) {
			err = fmt.Errorf("%w: %s", state.ErrInvalid, usage.msg)
		}
		if !r.rec.done {
			return board.APISkillsRun{}, err
		}
		out := board.APISkillsRun{Scope: a.Scope, Project: a.Project, Results: []board.APISkillResult{}, Refused: r.rec.refused, Reconcile: r.rec.goals}
		for _, row := range r.rec.rows {
			out.Results = append(out.Results, board.APISkillResult{Name: row[0], State: row[1], Detail: clean(row[2])})
		}
		for _, name := range slices.Sorted(maps.Keys(r.rec.diffs)) {
			diff, cut := capDiff(r.rec.diffs[name])
			out.Diffs = append(out.Diffs, board.APISkillDiff{Name: name, Diff: diff, Cut: cut})
		}
		return out, nil
	}
}
