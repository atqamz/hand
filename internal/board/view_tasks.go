package board

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type taskGroup struct {
	Name   string
	Count  int
	Checks []check
}

type tasksView struct {
	Base          string
	Live          bool
	Multi         bool
	All           bool
	Groups        []taskGroup
	Hidden        int
	FinishedCount int
}

func (b *Board) buildTasks(ctx context.Context, q url.Values, f facts, c viewContext, live bool) (tasksView, error) {
	v := tasksView{Base: c.Base, Live: live, Multi: c.Multi, All: q.Get("all") == "1"}
	groups := []string{state.StatusActive, state.StatusInbox}
	if v.All {
		groups = append(groups, state.StatusDone, state.StatusAbandoned)
	}
	var tasks []state.Task
	for _, status := range groups {
		if len(tasks) >= maxCards {
			break
		}
		part, err := b.st.Tasks(ctx, []string{status}, maxCards-len(tasks))
		if err != nil {
			return v, err
		}
		tasks = append(tasks, part...)
	}
	shown := len(tasks)
	if !v.All {
		recent, _, err := b.st.SearchTasks(ctx, state.TaskQuery{Statuses: finished, Limit: railFinished})
		if err != nil {
			return v, err
		}
		tasks = append(tasks, recent...)
	}
	delivered, err := b.deliverables(ctx, tasks)
	if err != nil {
		return v, err
	}
	f.delivered = delivered
	checks := make([]check, 0, len(tasks))
	for _, t := range tasks {
		checks = append(checks, f.check(t))
	}
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return v, err
	}
	total := counts[state.StatusActive] + counts[state.StatusInbox]
	if v.All {
		total += counts[state.StatusDone] + counts[state.StatusAbandoned]
	}
	subs, err := b.st.ChildCounts(ctx)
	if err != nil {
		return v, err
	}
	known := map[int64]bool{}
	exists := func(id int64) bool {
		ok, seen := known[id]
		if !seen {
			_, err := b.st.Task(ctx, id)
			ok = !errors.Is(err, state.ErrNotFound)
			known[id] = ok
		}
		return ok
	}
	for _, status := range groups {
		g := taskGroup{Name: strings.ToUpper(status[:1]) + status[1:], Count: counts[status]}
		for _, c := range checks[:shown] {
			if c.Task.Status == status {
				g.Checks = append(g.Checks, c)
			}
		}
		if len(g.Checks) > 0 {
			g.Checks = nest(g.Checks, subs, exists)
			v.Groups = append(v.Groups, g)
		}
	}
	if recent := checks[shown:]; len(recent) > 0 {
		v.Groups = append(v.Groups, taskGroup{Name: "Finished", Count: counts[state.StatusDone] + counts[state.StatusAbandoned], Checks: nest(recent, subs, exists)})
	}
	v.Hidden, v.FinishedCount = total-shown, counts[state.StatusDone]+counts[state.StatusAbandoned]
	return v, nil
}

func nest(checks []check, subs map[int64]int, exists func(int64) bool) []check {
	shown := make(map[int64]bool, len(checks))
	for _, c := range checks {
		shown[c.Task.ID] = true
	}
	kids := map[int64][]check{}
	var roots []check
	for _, c := range checks {
		c.Subs = subs[c.Task.ID]
		p := c.Task.ParentID
		if shown[p] && p != c.Task.ID {
			kids[p] = append(kids[p], c)
			continue
		}
		if p != 0 && exists(p) {
			c.Under = p
		}
		roots = append(roots, c)
	}
	out := make([]check, 0, len(checks))
	seen := make(map[int64]bool, len(checks))
	var walk func(c check, depth int)
	walk = func(c check, depth int) {
		if seen[c.Task.ID] {
			return
		}
		seen[c.Task.ID] = true
		c.Depth = depth
		out = append(out, c)
		for _, k := range kids[c.Task.ID] {
			walk(k, depth+1)
		}
	}
	for _, c := range roots {
		walk(c, 0)
	}
	for _, c := range checks {
		c.Subs = subs[c.Task.ID]
		walk(c, 0)
	}
	return out
}

type archiveView struct {
	Base     string
	Live     bool
	Multi    bool
	Groups   []taskGroup
	Total    int
	Projects []state.Project
	Chips    []chip
	Q        string
	Scope    string
	Scopes   []scopeOption
	Project  string
	Since    string
	Until    string
	Prev     string
	Next     string
}

func (b *Board) buildArchive(ctx context.Context, v url.Values) (archiveView, error) {
	q := strings.TrimSpace(v.Get("q"))
	statuses, scope := finished, ""
	switch s := v.Get("status"); {
	case s == "all":
		statuses, scope = nil, s
	case s == "" && q != "":
		statuses = nil
	case slices.ContainsFunc(scopes[3:], func(o scopeOption) bool { return o.Value == s }):
		statuses, scope = []string{s}, s
	case s != "":
		scope = "finished"
	}
	page := 1
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 && n <= maxArchivePage {
		page = n
	}
	query := state.TaskQuery{Q: q, Statuses: statuses, Project: v.Get("project"), Since: v.Get("since"), Until: v.Get("until"), Limit: archivePage, Offset: (page - 1) * archivePage}
	tasks, total, err := b.st.SearchTasks(ctx, query)
	if err != nil {
		return archiveView{}, err
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		return archiveView{}, err
	}
	f, err := b.facts(ctx)
	if err != nil {
		return archiveView{}, err
	}
	if f.delivered, err = b.deliverables(ctx, tasks); err != nil {
		return archiveView{}, err
	}
	out := archiveView{
		Base:     b.o.Base,
		Multi:    len(projects) > 1,
		Total:    total,
		Projects: projects,
		Q:        query.Q,
		Scope:    scope,
		Scopes:   scopes,
		Project:  query.Project,
		Since:    query.Since,
		Until:    query.Until,
	}
	for _, t := range tasks {
		c := f.check(t)
		at := t.FinishedAt
		if at == "" {
			at = t.UpdatedAt
		}
		label := parse(at).UTC().Format("Mon 2 Jan") + " UTC"
		if n := len(out.Groups); n == 0 || out.Groups[n-1].Name != label {
			out.Groups = append(out.Groups, taskGroup{Name: label})
		}
		g := &out.Groups[len(out.Groups)-1]
		g.Checks, g.Count = append(g.Checks, c), g.Count+1
	}
	keep := url.Values{}
	for _, k := range []string{"q", "status", "project", "since", "until"} {
		if s := strings.TrimSpace(v.Get(k)); s != "" {
			keep.Set(k, s)
		}
	}
	for _, c := range chipNames {
		val := keep.Get(c.key)
		if val == "" {
			continue
		}
		if c.key == "q" {
			val = "“" + val + "”"
		}
		if c.key == "status" {
			val = scopes[slices.IndexFunc(scopes, func(o scopeOption) bool { return o.Value == scope })].Label
		}
		rest := maps.Clone(keep)
		rest.Del(c.key)
		href := b.o.Base + "/tasks"
		if len(rest) > 0 {
			href += "?" + rest.Encode()
		}
		out.Chips = append(out.Chips, chip{Label: clip(c.name+" "+val, 40), Href: href})
	}
	link := func(n int) string {
		q := maps.Clone(keep)
		q.Set("page", strconv.Itoa(n))
		return b.o.Base + "/tasks?" + q.Encode()
	}
	if page > 1 {
		out.Prev = link(page - 1)
	}
	if query.Offset+len(tasks) < total {
		out.Next = link(page + 1)
	}
	return out, nil
}
