package board

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
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

var digest = regexp.MustCompile(`^[0-9a-f]{32}$`)

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
	data["Pending"], data["Profiles"], data["Keys"] = len(pending), b.profiles(), keyButtons()
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
			text, level, pct := gauge(st)
			data["Gauge"], data["Compactions"] = map[string]any{"Text": text, "Level": level, "Pct": pct}, st.Compactions
		}
		if sup.Status == state.AttemptRunning {
			data["Switchable"], data["SwitchProfiles"] = true, b.profilesFor(sup.Harness)
			data["CurrentModels"], data["CurrentEfforts"] = b.models(sup.Harness)
			data["OtherHarnesses"] = b.choices(sup.Harness)
		}
	}
	if data["Pick"] == true {
		data["StartHarnesses"] = b.choices("")
	}
	label, _ := data["PillLabel"].(string)
	data["Line"] = strings.ToLower(lineWord(label))
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

func gauge(s transcript.Status) (string, string, int) {
	if s.Context <= 0 {
		return "", "", 0
	}
	text := short(s.Context)
	if s.Window <= 0 {
		return text, "", 0
	}
	pct := int(math.Round(float64(s.Context) * 100 / float64(s.Window)))
	text += " / " + short(s.Window)
	switch {
	case pct >= 90:
		return text + " · compact soon", "fail", pct
	case pct >= 80:
		return text, "warn", pct
	}
	return text, "", pct
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
	case !sup.Live() && sup.Session != "":
		return "none", once(ref, "stopped", sup.Reason) + ". Resume continues its session and then delivers your message."
	case sup.Status != state.AttemptRunning:
		return "none", "No supervisor is running; your message waits until one starts."
	case stale:
		return "none", "Luvus restarted; your message waits until hand watch settles it."
	case blocked:
		return "blocked", ref + " is waiting on a screen in Needs; your message waits too."
	case sup.Switching():
		return agent, ref + " switches to " + sup.SwitchModel + " " + sup.SwitchEffort + " after this turn; your message waits for the new session."
	case agent == "working":
		return agent, ref + " is working; your message goes to it right away."
	}
	return agent, ref + " is ready; it reads your message now."
}

type working struct {
	Ref, Since, Label, On, From, FromLabel, Last, LastLabel string
	Input                                                   int64
}

func (b *Board) working(ctx context.Context, sup state.Supervisor) (working, error) {
	w := working{Ref: state.SupervisorRef(sup.ID), Since: sup.CreatedAt, On: "since it started"}
	e, ok, err := b.st.LatestEvent(ctx, "supervisor.delivered")
	if err != nil {
		return w, err
	}
	if ok && !parse(e.At).Before(parse(sup.CreatedAt)) {
		w.Since, w.On = e.At, "on a wake"
		ref, _, _ := strings.Cut(e.Detail, ": ")
		if id, err := strconv.ParseInt(strings.TrimPrefix(ref, "i"), 10, 64); err == nil && strings.HasPrefix(ref, "i") {
			in, err := b.st.SupervisorInput(ctx, id)
			if err != nil {
				return w, err
			}
			w.On, w.From, w.FromLabel, w.Input = "on your message from", in.CreatedAt, parse(in.CreatedAt).UTC().Format("15:04"), in.ID
		}
	}
	w.Label = elapsed(b.now().Sub(parse(w.Since)))
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
	s, err := b.o.Luvus.Read(ctx, sup.PaneID, luvus.ScreenLines)
	if err != nil || s.TerminalID != sup.TerminalID {
		data["Stale"] = staleServer
		return
	}
	data["Blocked"], data["Screen"], data["Revision"], data["Digest"] = true, s.Text, s.ContentRevision, luvus.ScreenDigest(s.Text)
}

type harnessChoice struct {
	Name    string
	Models  []harness.Model
	Efforts []string
}

func (b *Board) choices(except string) []harnessChoice {
	var out []harnessChoice
	for _, h := range state.Harnesses {
		if h != except {
			models, efforts := b.models(h)
			out = append(out, harnessChoice{Name: h, Models: models, Efforts: efforts})
		}
	}
	return out
}

func (b *Board) models(name string) ([]harness.Model, []string) {
	models, err := harness.Models(name, b.o.Harness)
	if err != nil {
		return nil, nil
	}
	var efforts []string
	for _, m := range models {
		for _, e := range m.Efforts {
			if !slices.Contains(efforts, e) {
				efforts = append(efforts, e)
			}
		}
	}
	return models, efforts
}

type profile struct{ Name, Label string }

func (b *Board) profiles() []profile { return b.profilesFor("") }

func (b *Board) profilesFor(name string) []profile {
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil {
		return nil
	}
	var out []profile
	for _, n := range p.Names() {
		s := p.Profiles[n]
		if name == "" || s.Harness == name {
			out = append(out, profile{Name: n, Label: strings.Join(strings.Fields(n+" · "+s.Harness+" "+s.Model+" "+s.Effort), " ")})
		}
	}
	return out
}

func (b *Board) timelineData(ctx context.Context, data map[string]any, q url.Values) error {
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil {
		return err
	}
	entries, note, sups, err := b.sessions(ctx)
	if err != nil {
		return err
	}
	if note != "" {
		data["Note"] = note
	}
	if _, stale := data["Stale"]; ok && sup.Status == state.AttemptRunning && data["Agent"] == "working" && !stale {
		w, err := b.working(ctx, sup)
		if err != nil {
			return err
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Role == "supervisor" {
				w.Last, w.LastLabel = entries[i].At, elapsed(b.now().Sub(parse(entries[i].At)))
				break
			}
		}
		data["Working"] = w
	}
	pending, err := b.st.PendingSupervisorInputs(ctx)
	if err != nil {
		return err
	}
	delivered, err := b.st.DeliveredSupervisorInputs(ctx, deliveredLookback)
	if err != nil {
		return err
	}
	sent := map[string][]state.SupervisorInput{}
	for _, in := range delivered {
		sent[strings.TrimSpace(in.Body)] = append(sent[strings.TrimSpace(in.Body)], in)
	}
	paired := map[int]state.SupervisorInput{}
	for i := len(entries) - 1; i >= 0; i-- {
		if k := strings.TrimSpace(entries[i].Text); entries[i].Role == "operator" && len(sent[k]) > 0 {
			paired[i], sent[k] = sent[k][0], sent[k][1:]
		}
	}
	models := map[string]string{}
	for _, s := range sups {
		models[state.SupervisorRef(s.ID)] = strings.TrimSpace(cmp.Or(s.Model, s.Harness) + " " + s.Effort)
	}
	all := make([]chatItem, 0, len(entries)+len(pending))
	no := 0
	for i, e := range entries {
		if e.Role == "hand" && launched.MatchString(e.Text) {
			continue
		}
		no++
		d := chatItem{Entry: e, Ref: refAt(sups, e.At), No: no}
		if in, ok := paired[i]; ok {
			d.Input, d.Delivered, d.Delivery = in.ID, in.DeliveredAt, "delivered"
			if in.Typed {
				d.Delivery = "typed anyway"
			}
		}
		all = append(all, d)
	}
	lines, err := b.lifecycle(ctx)
	if err != nil {
		return err
	}
	all = append(all, lines...)
	slices.SortStableFunc(all, func(x, y chatItem) int { return parse(x.At).Compare(parse(y.At)) })
	last := ""
	for i := range all {
		if all[i].Role == "supervisor" {
			if all[i].Ref != last {
				all[i].Model = models[all[i].Ref]
			}
			last = all[i].Ref
		}
	}
	all = groupWakes(all)
	for _, in := range pending {
		all = append(all, chatItem{Entry: transcript.Entry{Role: "operator", Text: in.Body, At: in.CreatedAt, Queued: true}, Input: in.ID, Delivery: "queued"})
	}
	for i := range all {
		if all[i].Role == "operator" {
			all[i].Text, all[i].Shots = shots(all[i].Text, b.o.Home)
		}
	}
	end := len(all)
	if n, err := strconv.Atoi(q.Get("before")); err == nil && n >= 0 && n < end {
		end = n
	}
	start := max(0, end-pageSize)
	page := slices.Clone(all[start:end])
	slices.Reverse(page)
	for i := 0; i+1 < len(page); i++ {
		if day := parse(page[i].At).Local(); day.Format(time.DateOnly) != parse(page[i+1].At).Local().Format(time.DateOnly) {
			page[i].Day = day.Format("Mon 2 Jan")
		}
	}
	data["Entries"], data["Older"] = page, start
	return nil
}

var launched = regexp.MustCompile(`^supervisor s[0-9]+ started$`)

const deliveredLookback = 50

var lifecycleKinds = []string{"supervisor.keys", "supervisor.started", "supervisor.stopped", "supervisor.exited", "supervisor.interrupted", "supervisor.failed", "supervisor.switch"}

func (b *Board) lifecycle(ctx context.Context) ([]chatItem, error) {
	events, err := b.st.RecentEventsOf(ctx, lifecycleKinds, historyLimit*4)
	if err != nil {
		return nil, err
	}
	sups, err := b.st.Supervisors(ctx)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]state.Supervisor, len(sups))
	for _, s := range sups {
		byRef[state.SupervisorRef(s.ID)] = s
	}
	out := make([]chatItem, 0, len(events))
	for _, e := range events {
		ref, detail, _ := strings.Cut(e.Detail, ": ")
		text := ""
		switch kind := strings.TrimPrefix(e.Kind, "supervisor."); kind {
		case "keys":
			text = "You pressed " + detail + " on " + ref + "'s screen"
		case "started":
			sup := byRef[ref]
			text = strings.Join(strings.Fields(ref+" started · "+sup.Harness+" "+sup.Model+" "+sup.Effort), " ")
		case "switch":
			if detail == "canceled" {
				text = ref + "'s switch was canceled"
			} else {
				text = ref + " will switch " + detail + " after this turn"
			}
		default:
			text = once(ref, kind, detail)
		}
		out = append(out, chatItem{Entry: transcript.Entry{Role: "hand", Text: text, At: e.At}, Ref: ref})
	}
	return out, nil
}

type chatItem struct {
	transcript.Entry
	No                  int
	Ref, Model, Day     string
	Delivered, Delivery string
	Input               int64
	Wakes               []chatItem
	Shots               []string
}

func once(ref, kind, detail string) string {
	if strings.HasPrefix(detail, kind+" ") {
		return ref + " " + detail
	}
	return ref + " " + kind + ": " + detail
}

func groupWakes(all []chatItem) []chatItem {
	wake := func(d chatItem) bool { return d.Role == "hand" && strings.HasPrefix(d.Text, "wake:") }
	out := make([]chatItem, 0, len(all))
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && wake(all[j]) {
			j++
		}
		if j-i < 2 {
			out = append(out, all[i])
			i++
			continue
		}
		out = append(out, chatItem{Entry: transcript.Entry{Role: "wakes", At: all[j-1].At}, No: all[j-1].No, Wakes: slices.Clone(all[i:j])})
		i = j
	}
	return out
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

func (b *Board) sessions(ctx context.Context) ([]transcript.Entry, string, []state.Supervisor, error) {
	sups, err := b.st.Supervisors(ctx)
	if err != nil || len(sups) == 0 {
		return nil, "no supervisor yet", nil, err
	}
	latest := sups[len(sups)-1]
	var all []transcript.Entry
	note := ""
	seen := map[string]bool{}
	for _, s := range sups {
		key := s.Harness + ":" + s.Session
		if s.Session == "" || seen[key] {
			continue
		}
		seen[key] = true
		entries, n := b.conversation(ctx, s, true)
		if s.Session == latest.Session && s.Harness == latest.Harness {
			note = n
		}
		all = append(all, entries...)
	}
	if latest.Session == "" {
		_, note = b.conversation(ctx, latest, true)
	}
	return all, note, sups, nil
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

func (b *Board) run(w http.ResponseWriter, r *http.Request, receipt string, args ...string) {
	if err := b.o.Control(r.Context(), args...); err != nil {
		b.failErr(w, err)
		return
	}
	b.done(w, r, b.o.Base+"/", receipt)
}

func (b *Board) done(w http.ResponseWriter, r *http.Request, next, receipt string) {
	if r.Header.Get("X-Hand-Fetch") != "1" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	if receipt != "" {
		w.Header().Set("X-Hand-Receipt", url.PathEscape(receipt))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *Board) supRef(ctx context.Context) (string, bool) {
	sup, ok, err := b.st.LatestSupervisor(ctx)
	if err != nil || !ok {
		return "the supervisor", false
	}
	return state.SupervisorRef(sup.ID), sup.Status == state.AttemptRunning
}

var simpleReceipts = map[string]string{"resume": "Starting the supervisor…", "stop": "%s stopping", "interrupt": "Interrupt sent to %s", "force": "Typed into %s"}

func (b *Board) simple(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if b.allowed(w) {
			ref, _ := b.supRef(r.Context())
			receipt := simpleReceipts[verb]
			if strings.Contains(receipt, "%s") {
				receipt = fmt.Sprintf(receipt, ref)
			}
			b.run(w, r, receipt, "supervisor", verb)
		}
	}
}

func (b *Board) start(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	args := []string{"supervisor", "start"}
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
	b.run(w, r, "Starting the supervisor…", args...)
}

func (b *Board) keys(w http.ResponseWriter, r *http.Request) {
	b.press(w, r, "supervisor", "")
}

func (b *Board) attemptKeys(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if n, err := strconv.ParseInt(strings.TrimPrefix(ref, "a"), 10, 64); err != nil || n < 1 || !strings.HasPrefix(ref, "a") {
		b.fail(w, http.StatusBadRequest, "press keys on an attempt shown by this page")
		return
	}
	b.press(w, r, "attempt", ref)
}

func (b *Board) press(w http.ResponseWriter, r *http.Request, group, target string) {
	if !b.allowed(w) {
		return
	}
	k, rev := r.PostFormValue("key"), r.PostFormValue("revision")
	if n, err := strconv.ParseInt(rev, 10, 64); err != nil || n < 0 || !slices.Contains(state.SupervisorKeys, k) {
		b.fail(w, http.StatusBadRequest, "press one of the listed keys on a screen shown by this page")
		return
	}
	args := []string{group, "keys", "--revision", rev}
	if d := r.PostFormValue("screen"); digest.MatchString(d) {
		args = append(args, "--screen", d)
	}
	if a, err := strconv.ParseInt(r.PostFormValue("after"), 10, 64); err == nil && a >= 0 {
		args = append(args, "--after", strconv.FormatInt(a, 10))
	}
	to := target
	if target != "" {
		args = append(args, target)
	} else {
		to, _ = b.supRef(r.Context())
	}
	if err := b.o.Control(r.Context(), append(args, k)...); err != nil {
		if errors.Is(err, state.ErrConflict) {
			b.fail(w, http.StatusConflict, staleScreen)
			return
		}
		b.failErr(w, err)
		return
	}
	b.done(w, r, b.o.Base+"/", "Sent "+k+" to "+to)
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
	b.run(w, r, "", "supervisor", "send", "--text", text)
}

func (b *Board) switchModel(w http.ResponseWriter, r *http.Request) {
	if !b.allowed(w) {
		return
	}
	args := []string{"supervisor", "switch"}
	switch {
	case r.PostFormValue("cancel") == "1":
		args = append(args, "--cancel")
	case r.PostFormValue("profile") != "":
		args = append(args, "--profile", r.PostFormValue("profile"))
	default:
		for _, f := range []string{"harness", "model", "effort"} {
			if v := strings.TrimSpace(r.PostFormValue(f)); v != "" {
				args = append(args, "--"+f, v)
			}
		}
	}
	if len(args) == 2 {
		b.fail(w, http.StatusBadRequest, "pick a profile or give a model or effort")
		return
	}
	receipt := "Switch set for after this turn"
	if h := strings.TrimSpace(r.PostFormValue("harness")); h != "" {
		receipt = "Switching to " + h + "…"
	}
	b.run(w, r, receipt, args...)
}
