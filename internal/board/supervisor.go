package board

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const (
	pageSize    = 50
	staleServer = "luvus restarted; hand watch settles this"
	staleScreen = "the screen changed, try again"
	controlsOff = "supervisor controls work only on a board that listens on loopback"
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

func (b *Board) fleet(w http.ResponseWriter, r *http.Request) {
	data, err := b.fleetData(r.Context(), r.URL.Query())
	if err != nil {
		b.failErr(w, err)
		return
	}
	data["Page"] = "fleet"
	b.render(w, http.StatusOK, "index.html", data)
}

func (b *Board) fleetData(ctx context.Context, q url.Values) (map[string]any, error) {
	data := map[string]any{"Title": "board", "All": q.Get("all") == "1", "Controls": b.o.Controls, "Token": b.token}
	for _, part := range []func(context.Context, map[string]any, url.Values) error{b.statusData, b.timelineData, b.queueData, b.tasksData} {
		if err := part(ctx, data, q); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (b *Board) history(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Title": "conversation"}
	if err := b.timelineData(r.Context(), data, r.URL.Query()); err != nil {
		b.failErr(w, err)
		return
	}
	b.render(w, http.StatusOK, "log.html", data)
}

func (b *Board) statusData(ctx context.Context, data map[string]any, q url.Values) error {
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return err
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return err
	}
	data["Pending"], data["Harnesses"], data["Profiles"], data["Keys"] = len(pending), state.Harnesses, b.profiles(), keyButtons()
	data["Pick"] = q.Get("pick") == "1" || !ok
	data["Pill"], data["PillLabel"] = "neutral", "No supervisor yet"
	if ok {
		data["Sup"], data["Ref"] = sup, state.SupervisorRef(sup.ID)
		data["Resumable"] = !sup.Live() && sup.Session != ""
		if sup.Status == state.AttemptRunning {
			b.live(ctx, sup, data)
		}
		data["Pill"], data["PillLabel"] = pill(sup, data)
	}
	return nil
}

func pill(sup state.Supervisor, data map[string]any) (string, string) {
	_, stale := data["Stale"]
	blocked, _ := data["Blocked"].(bool)
	switch {
	case sup.Status == state.AttemptRunning && stale:
		return "failing", "unreachable"
	case blocked:
		return "failing", "blocked"
	case sup.Live():
		return "running", sup.Status
	case sup.Status == state.AttemptStopped:
		return "neutral", sup.Status
	}
	return "failing", sup.Status
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

func (b *Board) timelineData(ctx context.Context, data map[string]any, q url.Values) error {
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return err
	}
	entries, note := b.conversation(ctx, sup, ok)
	if note != "" {
		data["Note"] = note
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return err
	}
	for _, in := range pending {
		entries = append(entries, transcript.Entry{Role: "operator", Text: in.Body, At: in.CreatedAt, Queued: true})
	}
	end := len(entries)
	if n, err := strconv.Atoi(q.Get("before")); err == nil && n >= 0 && n < end {
		end = n
	}
	start := max(0, end-pageSize)
	page := slices.Clone(entries[start:end])
	slices.Reverse(page)
	data["Entries"], data["Older"] = page, start
	return nil
}

func (b *Board) conversation(ctx context.Context, sup state.Supervisor, ok bool) ([]transcript.Entry, string) {
	switch {
	case !ok:
		return nil, "no supervisor yet"
	case b.o.Transcript == nil:
		return nil, transcript.ErrUnreadable.Error()
	}
	entries, err := b.o.Transcript.Read(ctx, sup.Harness, sup.Session, b.o.Home)
	if err != nil {
		return nil, Scrub(err.Error())
	}
	return entries, ""
}

func (b *Board) allowed(w http.ResponseWriter) bool {
	if b.o.Controls && b.o.Control != nil {
		return true
	}
	b.fail(w, http.StatusForbidden, controlsOff)
	return false
}

func (b *Board) run(w http.ResponseWriter, r *http.Request, args ...string) {
	if err := b.o.Control(r.Context(), args...); err != nil {
		b.failErr(w, err)
		return
	}
	http.Redirect(w, r, b.o.Base+"/", http.StatusSeeOther)
}

func (b *Board) simple(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b.allowed(w) {
			b.run(w, r, verb)
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
	b.run(w, r, args...)
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
	http.Redirect(w, r, b.o.Base+"/", http.StatusSeeOther)
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
	b.run(w, r, "send", "--text", text)
}
