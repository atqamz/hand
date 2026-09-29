package board

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

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

func (b *Board) stripData(ctx context.Context) map[string]any {
	data := map[string]any{}
	if b.statusData(ctx, data, url.Values{}) != nil {
		return nil
	}
	return data
}

func (b *Board) fleetData(ctx context.Context, q url.Values) (map[string]any, error) {
	data := map[string]any{"Title": "board", "All": q.Get("all") == "1", "Controls": b.o.Controls, "Token": b.token, "Live": true}
	f, err := b.facts(ctx)
	if err != nil {
		return nil, err
	}
	data["facts"] = f
	for _, part := range []func(context.Context, map[string]any, url.Values) error{b.statusData, b.timelineData, b.queueData, b.tasksData} {
		if err := part(ctx, data, q); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (b *Board) history(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Title": "conversation", "StripData": b.stripData(r.Context())}
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
	data["AgentState"], data["ComposerHint"] = "none", "No supervisor is running; your message waits until one starts."
	if ok {
		data["Sup"], data["Ref"] = sup, state.SupervisorRef(sup.ID)
		data["Resumable"] = !sup.Live() && sup.Session != ""
		if sup.Status == state.AttemptRunning {
			b.live(ctx, sup, data)
		}
		data["Pill"], data["PillLabel"] = pill(sup, data)
		data["AgentState"], data["ComposerHint"] = hint(sup, data)
		if b.o.Transcript != nil {
			b.conversation(ctx, sup, ok)
			st := b.o.Transcript.Status(sup.Harness, sup.Session)
			text, level := gauge(st)
			data["Gauge"], data["Compactions"] = map[string]string{"Text": text, "Level": level}, st.Compactions
		}
		if sup.Status == state.AttemptRunning && (sup.Harness == "claude" || sup.Harness == "codex") {
			data["Switchable"], data["SwitchProfiles"] = true, b.profilesFor(sup.Harness)
		}
	}
	label, _ := data["PillLabel"].(string)
	data["Line"] = lineWord(label)
	return nil
}

func lineWord(label string) string {
	switch label {
	case "No supervisor yet":
		return "NO SUPERVISOR"
	case "launching":
		return "STARTING"
	}
	return strings.ToUpper(label)
}

func gauge(s transcript.Status) (string, string) {
	if s.Context <= 0 {
		return "", ""
	}
	text := "CTX " + short(s.Context)
	if s.Window <= 0 {
		return text, ""
	}
	pct := int(math.Round(float64(s.Context) * 100 / float64(s.Window)))
	text += " / " + short(s.Window) + " · " + strconv.Itoa(pct) + "%"
	switch {
	case pct >= 90:
		return text + " COMPACT SOON", "flash"
	case pct >= 80:
		return text, "warn"
	}
	return text, ""
}

func short(n int64) string {
	unit, f := "", float64(n)
	switch {
	case n >= 999_500:
		unit, f = "M", f/1e6
	case n >= 1000:
		unit, f = "K", f/1e3
	default:
		return strconv.FormatInt(n, 10)
	}
	d := 2
	if f >= 99.95 {
		d = 0
	} else if f >= 9.995 {
		d = 1
	}
	out := strconv.FormatFloat(f, 'f', d, 64)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(strings.TrimRight(out, "0"), ".")
	}
	return out + unit
}

func pill(sup state.Supervisor, data map[string]any) (string, string) {
	_, stale := data["Stale"]
	blocked, _ := data["Blocked"].(bool)
	switch {
	case sup.Status == state.AttemptRunning && stale:
		return "failing", "unreachable"
	case blocked:
		return "failing", "blocked"
	case sup.Status == state.AttemptLaunching:
		return "neutral", "launching"
	case sup.Status == state.AttemptRunning && data["Agent"] == "working":
		return "working", "working"
	case sup.Status == state.AttemptRunning:
		return "ready", "ready"
	case sup.Status == state.AttemptStopped:
		return "neutral", sup.Status
	}
	return "failing", sup.Status
}

func hint(sup state.Supervisor, data map[string]any) (string, string) {
	ref := state.SupervisorRef(sup.ID)
	_, stale := data["Stale"]
	blocked, _ := data["Blocked"].(bool)
	agent := "ready"
	if data["Agent"] == "working" {
		agent = "working"
	}
	switch {
	case sup.Status == state.AttemptLaunching:
		return "none", ref + " is starting; your message waits."
	case sup.Status != state.AttemptRunning:
		return "none", "No supervisor is running; your message waits until one starts."
	case stale:
		return "none", "Luvus restarted; your message waits until hand watch settles it."
	case blocked:
		return "blocked", ref + " is waiting on a screen in Needs you; your message waits too."
	case sup.Switching():
		return agent, ref + " switches to " + sup.SwitchModel + " " + sup.SwitchEffort + " after this turn; your message waits for the new session."
	case agent == "working":
		return agent, ref + " is working; your message goes to it right away."
	}
	return agent, ref + " is ready; it reads your message now."
}

type working struct {
	Ref, Since, Label, On, From, FromLabel string
}

func (b *Board) working(ctx context.Context, sup state.Supervisor) (working, error) {
	w := working{Ref: state.SupervisorRef(sup.ID), Since: sup.CreatedAt, On: "since it started"}
	e, ok, err := b.st.LatestEvent(ctx, "supervisor.delivered")
	if err != nil {
		return w, err
	}
	if ok && !parse(e.At).Before(parse(sup.CreatedAt)) {
		w.Since, w.On = e.At, "on a wake"
		if id, err := strconv.ParseInt(strings.TrimPrefix(e.Detail, "i"), 10, 64); err == nil && strings.HasPrefix(e.Detail, "i") {
			in, err := b.st.SupervisorInput(ctx, id)
			if err != nil {
				return w, err
			}
			w.On, w.From, w.FromLabel = "on your message from", in.CreatedAt, parse(in.CreatedAt).UTC().Format("15:04")
		}
	}
	w.Label = elapsed(time.Since(parse(w.Since)))
	return w, nil
}

func parse(stamp string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, stamp)
	return t
}

func elapsed(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m < 1:
		return "<1m"
	case m < 60:
		return strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m/60) + "h " + strconv.Itoa(m%60) + "m"
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

func (b *Board) profilesFor(name string) []string {
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range p.Names() {
		if p.Profiles[n].Harness == name {
			out = append(out, n)
		}
	}
	return out
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
	if _, stale := data["Stale"]; ok && sup.Status == state.AttemptRunning && data["Agent"] == "working" && !stale {
		w, err := b.working(ctx, sup)
		if err != nil {
			return err
		}
		data["Working"] = w
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return err
	}
	var sups []state.Supervisor
	if ok && sup.Session != "" {
		if sups, err = b.st.SessionSupervisors(ctx, sup.Session); err != nil {
			return err
		}
	}
	numbered := len(entries)
	for _, in := range pending {
		entries = append(entries, transcript.Entry{Role: "operator", Text: in.Body, At: in.CreatedAt, Queued: true})
	}
	end := len(entries)
	if n, err := strconv.Atoi(q.Get("before")); err == nil && n >= 0 && n < end {
		end = n
	}
	start := max(0, end-pageSize)
	page := make([]dispatch, 0, end-start)
	for i := start; i < end; i++ {
		d := dispatch{Entry: entries[i], Ref: refAt(sups, entries[i].At)}
		if i < numbered {
			d.No = i + 1
		}
		page = append(page, d)
	}
	slices.Reverse(page)
	data["Entries"], data["Older"] = page, start
	return nil
}

type dispatch struct {
	transcript.Entry
	No  int
	Ref string
}

func refAt(sups []state.Supervisor, at string) string {
	if len(sups) == 0 {
		return ""
	}
	ref, when := sups[0].ID, parse(at)
	for _, s := range sups[1:] {
		if !when.IsZero() && !parse(s.CreatedAt).After(when) {
			ref = s.ID
		}
	}
	return state.SupervisorRef(ref)
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

func (b *Board) switchModel(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	args := []string{"switch"}
	switch {
	case r.PostFormValue("cancel") == "1":
		args = append(args, "--cancel")
	case r.PostFormValue("profile") != "":
		args = append(args, "--profile", r.PostFormValue("profile"))
	default:
		for _, f := range []string{"model", "effort"} {
			if v := strings.TrimSpace(r.PostFormValue(f)); v != "" {
				args = append(args, "--"+f, v)
			}
		}
	}
	if len(args) == 1 {
		b.fail(w, http.StatusBadRequest, "pick a profile or give a model or effort")
		return
	}
	b.run(w, r, args...)
}
