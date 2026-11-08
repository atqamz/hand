package board

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

func shown(s string) string {
	var out strings.Builder
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		if bidi(r) || unicode.IsControl(r) && r != '\n' && r != '\t' || r == 0x2028 || r == 0x2029 {
			fmt.Fprintf(&out, "U+%04X", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func attemptState(a state.Attempt, done map[int64]bool) string {
	switch {
	case a.Live():
		return "running"
	case facts{done: done}.failing(a):
		return "failing"
	case done[a.ID]:
		return "passing"
	}
	return "idle"
}

func (b *Board) attempt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "a")
	if err != nil {
		b.failErr(w, err)
		return
	}
	v, err := b.buildAttempt(ctx, id)
	if err != nil {
		b.failErr(w, err)
		return
	}
	b.render(w, http.StatusOK, "attempt.html", map[string]any{
		"Title": state.AttemptRef(id), "Page": "attempt", "Fleets": b.fleets(ctx), "Detail": v,
		"StripData": b.stripData(ctx), "Token": b.csrf(ctx), "Titles": b.refTitle(ctx),
	})
}

type workerView struct {
	Turns       []transcript.Entry
	More        bool
	Note, Model string
	Answered    string
	Shown       bool
}

func (b *Board) workerTurns(ctx context.Context, a state.Attempt) workerView {
	if a.Harness != "copilot" || b.o.Transcript == nil {
		return workerView{}
	}
	v := workerView{Shown: true}
	i := slices.Index(a.Argv, "--session-id")
	if i < 0 || i+1 >= len(a.Argv) {
		v.Note = "The launch line has no session id, so there is no transcript to read."
		return v
	}
	id := a.Argv[i+1]
	entries, err := b.o.Transcript.Read(ctx, a.Harness, id, b.o.Home)
	if err != nil {
		v.Note = Scrub(err.Error())
		return v
	}
	if n := len(entries) - historyLimit; n > 0 {
		entries, v.More = entries[n:], true
	}
	v.Turns = entries
	st := b.o.Transcript.Status(a.Harness, id)
	v.Answered = st.Model
	if st.Model != "" && st.Selected != "" && !strings.EqualFold(st.Selected, "auto") && st.Selected != st.Model {
		v.Model = "Asked for " + st.Selected + ", answered by " + st.Model + "."
	}
	return v
}
