package board

import (
	"cmp"
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
	Source  string
	Ref     string
	Phone   bool
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
	Source  string `json:"source"`
	Ref     string `json:"ref"`
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

func (b *Board) skillsContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, skillsWait)
	if b.o.Lifetime == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(b.o.Lifetime, cancel)
	return ctx, func() { stop(); cancel() }
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
	ctx, cancel := b.skillsContext(r.Context())
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
	s, _ := r.Context().Value(sessionKey{}).(session)
	phone := s.Device != nil
	if phone && verb != "sync" {
		b.apiFail(w, http.StatusForbidden, "forbidden", "a paired Companion cannot update, propose, add or remove skills; use the host's Board")
		return
	}
	var in apiSkillsBody
	const bad = "send one JSON object with scope, project (for scope project), name, for sync or add yes, and for add source and ref"
	if !b.apiObject(w, r, &in, bad) {
		return
	}
	in.Name, in.Yes, in.Source, in.Ref = strings.TrimSpace(in.Name), strings.TrimSpace(in.Yes), strings.TrimSpace(in.Source), strings.TrimSpace(in.Ref)
	switch msg := skillsScope(in.Scope, in.Project, true); {
	case msg != "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", msg)
		return
	case verb == "propose" && in.Name == "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "propose needs the name of one skill")
		return
	case verb == "remove" && in.Name == "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "remove needs the name of one skill")
		return
	case verb == "add" && in.Source == "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "add needs the source of one skill")
		return
	case verb == "add" && in.Name != "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "add takes a source, not a name")
		return
	case verb != "add" && (in.Source != "" || in.Ref != ""):
		b.apiFail(w, http.StatusBadRequest, "bad_request", "source and ref go only with add")
		return
	case verb != "sync" && verb != "add" && in.Yes != "":
		b.apiFail(w, http.StatusBadRequest, "bad_request", "yes goes only with sync or add")
		return
	case !skillsText(in.Name) || !skillsText(in.Yes) || !skillsText(in.Source) || !skillsText(in.Ref):
		b.apiFail(w, http.StatusBadRequest, "bad_request", "a name, yes, source or ref with a control character or over 512 bytes")
		return
	}
	switch {
	case phone && in.Yes != "":
		b.apiFail(w, http.StatusForbidden, "forbidden", "a paired Companion cannot apply a third-party update; read the diff, then tell the supervisor or use the host's Board")
		return
	case phone && in.Scope == "user":
		b.apiFail(w, http.StatusForbidden, "forbidden", "a paired Companion cannot sync the user scope; use the host's Board")
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
	kept := context.WithoutCancel(r.Context())
	ctx, cancel := b.skillsContext(kept)
	defer cancel()
	out, err := b.o.SkillsRun(ctx, SkillsAction{Verb: verb, Scope: in.Scope, Project: in.Project, Name: in.Name, Yes: in.Yes, Source: in.Source, Ref: in.Ref, Phone: phone})
	detail := ""
	switch {
	case verb == "sync":
		detail = skillsSynced(actor(kept, "operator (desktop)"), in, out, err)
	case err == nil && out.Refused == 0 && (verb == "remove" || verb == "add" && skillsAdded(out)):
		detail = skillsNamed(actor(kept, "operator (desktop)"), in)
	}
	if detail != "" {
		if werr := b.st.NoteSkills(kept, skillsEvents[verb], detail); werr != nil {
			b.apiFailErr(w, werr)
			return
		}
	}
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	apiReply{receipt: skillsReceipt(verb, out)}.send(w)
	b.apiOK(w, f, "", out)
}

var skillsEvents = map[string]string{"sync": "synced", "add": "added", "remove": "removed"}

func skillsAdded(out APISkillsRun) bool {
	return slices.ContainsFunc(out.Results, func(r APISkillResult) bool { return r.State == "added" })
}

func skillsNamed(who string, in apiSkillsBody) string {
	what := in.Scope
	if in.Project != "" {
		what += " " + in.Project
	}
	named := cmp.Or(in.Name, in.Source)
	if in.Yes != "" {
		named += " as " + in.Yes
	}
	return what + " by " + who + ": " + named
}

func skillsSynced(who string, in apiSkillsBody, out APISkillsRun, err error) string {
	what, res := in.Scope, skillsCounts(out)
	if err != nil {
		status, _ := failure(err)
		res = "failed " + apiCode(status)
	} else if in.Project != "" {
		what += " " + in.Project
	}
	return what + " by " + who + ": " + res
}

func skillsCounts(out APISkillsRun) string {
	counts := map[string]int{}
	for _, r := range out.Results {
		counts[r.State]++
	}
	parts := make([]string, 0, len(counts))
	for _, st := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
	}
	if len(parts) == 0 {
		return "nothing subscribed"
	}
	return strings.Join(parts, ", ")
}

func skillsReceipt(verb string, out APISkillsRun) string {
	return "Skills " + verb + ": " + skillsCounts(out)
}
