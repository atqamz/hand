package board

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const (
	pageSize     = 50
	staleServer  = "luvus restarted; hand watch settles this"
	staleScreen  = "the screen changed, try again"
	controlsOff  = "supervisor controls work only on a board that listens on loopback"
	liveRefresh  = 3
	cardsRefresh = 5
	endedRefresh = 10
)

type key struct{ Name, Label string }

var keyLabels = map[string]string{"enter": "Enter", "esc": "Esc", "up": "↑", "down": "↓"}

func keyButtons() []key {
	out := make([]key, 0, len(state.SupervisorKeys))
	for _, k := range state.SupervisorKeys {
		label := k
		if l, ok := keyLabels[k]; ok {
			label = l
		}
		out = append(out, key{k, label})
	}
	return out
}

func (b *Board) shell(w http.ResponseWriter, r *http.Request) {
	b.render(w, http.StatusOK, "index.html", map[string]any{
		"Title": "board", "All": r.URL.Query().Get("all") == "1", "Send": b.o.Controls, "Token": b.token,
	})
}

func (b *Board) panel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	pick := r.URL.Query().Get("pick") == "1"
	data := map[string]any{
		"Title": "supervisor", "Controls": b.o.Controls, "Token": b.token, "Pending": len(pending),
		"Harnesses": state.Harnesses, "Profiles": b.profiles(), "Keys": keyButtons(), "Pick": pick || !ok,
	}
	if ok {
		data["Sup"], data["Ref"] = sup, state.SupervisorRef(sup.ID)
		data["Resumable"] = !sup.Live() && sup.Session != ""
		switch {
		case sup.Live():
			data["Refresh"] = liveRefresh
		case !pick:
			data["Refresh"] = endedRefresh
		}
		if sup.Status == state.AttemptRunning {
			b.live(ctx, sup, data)
		}
	}
	b.render(w, http.StatusOK, "panel.html", data)
}

func (b *Board) live(ctx context.Context, sup state.Supervisor, data map[string]any) {
	caps, err := b.o.Luvus.Check(ctx)
	if err != nil || caps.ServerGeneration != sup.ServerGeneration {
		data["Stale"] = staleServer
		return
	}
	ag, err := b.o.Luvus.Explain(ctx, sup.PaneID)
	if err != nil {
		data["Stale"] = staleServer
		return
	}
	data["Agent"], data["Hint"] = ag.Status, ag.Hint
	if ag.Status != "blocked" {
		return
	}
	s, err := b.o.Luvus.Read(ctx, sup.PaneID, 20)
	if err != nil || s.TerminalID != sup.TerminalID {
		data["Stale"] = staleServer
		return
	}
	data["Blocked"], data["Screen"], data["Revision"] = true, s.Text, s.ContentRevision
}

func (b *Board) profiles() []string {
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil {
		return nil
	}
	return p.Names()
}

func (b *Board) log(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	live := r.URL.Query().Get("live") != "0" && !r.URL.Query().Has("before")
	data := map[string]any{"Title": "conversation", "Live": live}
	if live {
		data["Refresh"], data["RefreshURL"] = liveRefresh, "/supervisor/log"
	}
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	var entries []transcript.Entry
	switch {
	case !ok:
		data["Note"] = "no supervisor yet"
	case b.o.Transcript == nil:
		data["Note"] = transcript.ErrUnreadable.Error()
	default:
		entries, err = b.o.Transcript.Read(ctx, sup.Harness, sup.Session, b.o.Home)
		if err != nil {
			data["Note"] = Scrub(err.Error())
		}
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		b.failErr(w, err)
		return
	}
	for _, in := range pending {
		entries = append(entries, transcript.Entry{Role: "operator", Text: "queued: " + in.Body, At: in.CreatedAt, Queued: true})
	}
	end := len(entries)
	if n, err := strconv.Atoi(r.URL.Query().Get("before")); err == nil && n >= 0 && n < end {
		end = n
	}
	start := max(0, end-pageSize)
	page := slices.Clone(entries[start:end])
	slices.Reverse(page)
	data["Entries"], data["Older"], data["Paged"] = page, start, end < len(entries)
	b.render(w, http.StatusOK, "log.html", data)
}

func (b *Board) allowed(w http.ResponseWriter) bool {
	if b.o.Controls && b.o.Control != nil {
		return true
	}
	b.fail(w, http.StatusForbidden, controlsOff)
	return false
}

func (b *Board) run(w http.ResponseWriter, r *http.Request, back string, args ...string) {
	if err := b.o.Control(r.Context(), args...); err != nil {
		b.failErr(w, err)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (b *Board) simple(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b.allowed(w) {
			b.run(w, r, "/supervisor/panel", verb)
		}
	}
}

func (b *Board) start(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	args := []string{"start"}
	if p := r.PostFormValue("profile"); p != "" {
		args = append(args, "--profile", p)
	} else if h := r.PostFormValue("harness"); h != "" {
		args = append(args, "--harness", h)
		for _, f := range []string{"model", "effort"} {
			if v := r.PostFormValue(f); v != "" {
				args = append(args, "--"+f, v)
			}
		}
	}
	b.run(w, r, "/supervisor/panel", args...)
}

func (b *Board) keys(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	k, rev := r.PostFormValue("key"), r.PostFormValue("revision")
	if n, err := strconv.ParseInt(rev, 10, 64); err != nil || n < 0 || !slices.Contains(state.SupervisorKeys, k) {
		b.fail(w, http.StatusBadRequest, "press one of the listed keys on a screen shown by this page")
		return
	}
	if err := b.o.Control(r.Context(), "keys", "--revision", rev, k); err != nil {
		if errors.Is(err, state.ErrConflict) {
			b.fail(w, http.StatusConflict, staleScreen)
			return
		}
		b.failErr(w, err)
		return
	}
	http.Redirect(w, r, "/supervisor/panel", http.StatusSeeOther)
}

func (b *Board) send(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	text := strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	if err := state.CheckMessage(text); err != nil {
		b.failErr(w, err)
		return
	}
	b.run(w, r, "/", "send", "--text", text)
}
