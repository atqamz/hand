package board

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type scopeOption struct{ Value, Label string }

type chip struct{ Label, Href string }

var chipNames = []struct{ key, name string }{{"q", "Search"}, {"status", "Status"}, {"project", "Project"}, {"since", "Since"}, {"until", "Until"}}

var scopes = []scopeOption{
	{"", "Default"}, {"finished", "Done and abandoned"}, {"all", "Every status"},
	{state.StatusDone, "Done"}, {state.StatusAbandoned, "Abandoned"}, {state.StatusActive, "Active"}, {state.StatusInbox, "Inbox"},
}

func (b *Board) archive(w http.ResponseWriter, r *http.Request) {
	ctx, v := r.Context(), r.URL.Query()
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
		b.failErr(w, err)
		return
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	f, err := b.facts(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	if f.delivered, err = b.deliverables(ctx, tasks); err != nil {
		b.failErr(w, err)
		return
	}
	var groups []taskGroup
	for _, t := range tasks {
		c := f.check(t)
		c.At = t.FinishedAt
		label := parse(t.FinishedAt).UTC().Format("Mon 2 Jan") + " UTC"
		if n := len(groups); n == 0 || groups[n-1].Name != label {
			groups = append(groups, taskGroup{Name: label})
		}
		g := &groups[len(groups)-1]
		g.Checks, g.Count = append(g.Checks, c), g.Count+1
	}
	keep := url.Values{}
	for _, k := range []string{"q", "status", "project", "since", "until"} {
		if s := strings.TrimSpace(v.Get(k)); s != "" {
			keep.Set(k, s)
		}
	}
	var chips []chip
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
		chips = append(chips, chip{Label: clip(c.name+" "+val, 40), Href: href})
	}
	link := func(n int) string {
		q := maps.Clone(keep)
		q.Set("page", strconv.Itoa(n))
		return b.o.Base + "/tasks?" + q.Encode()
	}
	data := map[string]any{
		"Title": "tasks", "Page": "tasks", "Fleets": b.fleets(ctx), "StripData": b.stripData(ctx), "Token": b.token,
		"Groups": groups, "Total": total, "Projects": projects, "Chips": chips,
		"Q": query.Q, "Scope": scope, "Scopes": scopes, "Project": query.Project, "Since": query.Since, "Until": query.Until,
	}
	if page > 1 {
		data["Prev"] = link(page - 1)
	}
	if query.Offset+len(tasks) < total {
		data["Next"] = link(page + 1)
	}
	b.render(w, http.StatusOK, "tasks.html", data)
}
