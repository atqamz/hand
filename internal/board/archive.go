package board

import (
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

func (b *Board) archive(w http.ResponseWriter, r *http.Request) {
	ctx, v := r.Context(), r.URL.Query()
	statuses := finished
	if s := v.Get("status"); s == state.StatusDone || s == state.StatusAbandoned {
		statuses = []string{s}
	}
	page := 1
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 && n <= maxArchivePage {
		page = n
	}
	query := state.TaskQuery{Q: strings.TrimSpace(v.Get("q")), Statuses: statuses, Project: v.Get("project"), Since: v.Get("since"), Until: v.Get("until"), Limit: archivePage, Offset: (page - 1) * archivePage}
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
	if f.prs, err = b.prLinks(ctx, tasks); err != nil {
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
	link := func(n int) string {
		q := maps.Clone(keep)
		q.Set("page", strconv.Itoa(n))
		return b.o.Base + "/tasks?" + q.Encode()
	}
	data := map[string]any{
		"Title": "finished tasks", "Page": "tasks", "StripData": b.stripData(ctx), "Token": b.token,
		"Groups": groups, "Total": total, "Projects": projects, "Filtered": len(keep) > 0,
		"Q": query.Q, "Status": v.Get("status"), "Project": query.Project, "Since": query.Since, "Until": query.Until,
	}
	if page > 1 {
		data["Prev"] = link(page - 1)
	}
	if query.Offset+len(tasks) < total {
		data["Next"] = link(page + 1)
	}
	b.render(w, http.StatusOK, "tasks.html", data)
}
