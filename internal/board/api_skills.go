package board

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	skillsWait    = 5 * time.Minute
	skillsMaxText = 512
)

type SkillsQuery struct {
	Scope   string
	Project string
}

type SkillsAction struct {
	Verb    string
	Scope   string
	Project string
	Name    string
	Yes     string
}

type APISkill struct {
	Name    string `json:"name"`
	Source  string `json:"source,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Latest  string `json:"latest,omitempty"`
	Managed bool   `json:"managed"`
	Trusted bool   `json:"trusted"`
	State   string `json:"state,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Diff    string `json:"diff,omitempty"`
	DiffCut bool   `json:"diff_cut,omitempty"`
}

type APISkillScope struct {
	Scope   string     `json:"scope"`
	Project string     `json:"project,omitempty"`
	Error   string     `json:"error,omitempty"`
	Skills  []APISkill `json:"skills"`
}

type APISkills struct {
	Scopes []APISkillScope `json:"scopes"`
}

type APISkillResult struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type APISkillDiff struct {
	Name string `json:"name"`
	Diff string `json:"diff"`
	Cut  bool   `json:"cut,omitempty"`
}

type APISkillsRun struct {
	Scope     string           `json:"scope"`
	Project   string           `json:"project,omitempty"`
	Results   []APISkillResult `json:"results"`
	Refused   int              `json:"refused"`
	Diffs     []APISkillDiff   `json:"diffs,omitempty"`
	Reconcile []string         `json:"reconcile,omitempty"`
}

type apiSkillsBody struct {
	Scope   string `json:"scope"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Yes     string `json:"yes"`
}

var skillScopes = []string{"fleet", "project", "user"}

func skillsText(v string) bool {
	return len(v) <= skillsMaxText && !strings.ContainsFunc(v, unicode.IsControl)
}

func skillsScope(scope, project string, required bool) string {
	switch {
	case scope == "" && !required && project == "":
		return ""
	case !slices.Contains(skillScopes, scope):
		return "scope is fleet, project or user"
	case scope == "project" && project == "" && required:
		return "scope project needs a project name"
	case scope != "project" && project != "":
		return "project goes only with scope project"
	case !skillsText(project):
		return "the project name has a control character or is over 512 bytes"
	}
	return ""
}

func (b *Board) skillsGate(w http.ResponseWriter) bool {
	if b.o.Skills == nil || b.o.SkillsRun == nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the board's controls are off")
		return false
	}
	if !b.skillsBusy.TryLock() {
		b.apiFail(w, http.StatusConflict, "conflict", "another skills request is running on this board; try again when it ends")
		return false
	}
	return true
}

func (b *Board) apiSkills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope, project := q.Get("scope"), q.Get("project")
	if msg := skillsScope(scope, project, false); msg != "" {
		b.apiFail(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	f, since, ok := b.apiSince(w, r)
	if !ok || !b.skillsGate(w) {
		return
	}
	defer b.skillsBusy.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), skillsWait)
	defer cancel()
	data, err := b.o.Skills(ctx, SkillsQuery{Scope: scope, Project: project})
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiOK(w, f, since, data)
}

func (b *Board) apiSkillsAction(w http.ResponseWriter, r *http.Request, verb string) {
	if !b.apiCSRF(w, r) {
		return
	}
	if s, _ := r.Context().Value(sessionKey{}).(session); s.Device != nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "a paired Companion cannot change skills; use the host's Board")
		return
	}
	var in apiSkillsBody
	const bad = "send one JSON object with scope, project (for scope project), name and, for sync, yes"
	if !b.apiObject(w, r, &in, bad) {
		return
	}
	in.Name, in.Yes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Yes)
	switch msg := skillsScope(in.Scope, in.Project, true); {
	case msg != "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", msg)
		return
	case verb == "propose" && in.Name == "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "propose needs the name of one skill")
		return
	case verb != "sync" && in.Yes != "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "yes goes only with sync")
		return
	case !skillsText(in.Name) || !skillsText(in.Yes):
		b.apiFail(w, http.StatusBadRequest, "bad_request", "a name or yes with a control character or over 512 bytes")
		return
	}
	f, err := b.st.Fleet(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if !b.skillsGate(w) {
		return
	}
	defer b.skillsBusy.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), skillsWait)
	defer cancel()
	out, err := b.o.SkillsRun(ctx, SkillsAction{Verb: verb, Scope: in.Scope, Project: in.Project, Name: in.Name, Yes: in.Yes})
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	apiReply{receipt: skillsReceipt(verb, out)}.send(w)
	b.apiOK(w, f, "", out)
}

func skillsReceipt(verb string, out APISkillsRun) string {
	counts := map[string]int{}
	for _, r := range out.Results {
		counts[r.State]++
	}
	parts := make([]string, 0, len(counts))
	for _, st := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
	}
	if len(parts) == 0 {
		return "Skills " + verb + ": nothing subscribed"
	}
	return "Skills " + verb + ": " + strings.Join(parts, ", ")
}
