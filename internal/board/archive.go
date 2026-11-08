package board

import (
	"net/http"

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
	ctx := r.Context()
	v, err := b.buildArchive(ctx, r.URL.Query())
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.render(w, http.StatusOK, "tasks.html", map[string]any{
		"Title": "tasks", "Page": "tasks", "Fleets": b.fleets(ctx), "Detail": v,
		"StripData": b.stripData(ctx), "Token": b.csrf(ctx),
	})
}
