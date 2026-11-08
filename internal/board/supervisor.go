package board

import (
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
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const (
	pageSize    = 50
	staleServer = "Luvus restarted; hand watch settles this"
	staleScreen = "the screen changed, try again"
	controlsOff = "supervisor controls work only on a board that listens on loopback"
)

type key struct{ Name, Label string }

var keyLabels = map[string]string{"enter": "Enter", "esc": "Esc", "up": "↑", "down": "↓"}

var digest = regexp.MustCompile(`^[0-9a-f]{32}$`)

var (
	circleOption = regexp.MustCompile(`^(?:[❯›]\s*)?([◯◉○●])\s*(.+)$`)
	circleFocus  = regexp.MustCompile(`^[❯›]\s`)
	askHint      = regexp.MustCompile(`↑/↓|\besc cancel\b|·\s*n note`)
	dialogFooter = regexp.MustCompile(`(?i)↑/↓|\bselect\b|\besc\b.{0,12}\bcancel\b|\btab\b.{0,12}\bamend\b`)
)

type askChoice struct{ Label, Field, Value string }

type askCard struct {
	Question string
	Options  []askChoice
}

func numberedAsk(p prompt, screen string) *askCard {
	var out []askChoice
	for _, o := range p.Options {
		if _, err := strconv.Atoi(o.Name); err != nil {
			continue
		}
		out = append(out, askChoice{Label: o.Label, Field: "key", Value: o.Name})
	}
	if len(out) == 0 {
		return nil
	}
	question := p.Headline
	lines := strings.Split(screen, "\n")
	var clean []int
	for i, raw := range lines {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(raw, ""))
		if t == "" || strings.Trim(t, "─━╭╮╰╯┌┐└┘│┃ ") == "" || (!numbered.MatchString(t) && (hintLine.MatchString(t) || askHint.MatchString(t))) {
			continue
		}
		clean = append(clean, i)
	}
	text := func(k int) string { return strings.TrimSpace(boxEdge.ReplaceAllString(lines[clean[k]], "")) }
	end := -1
	for k := len(clean) - 1; k >= 0; k-- {
		if numbered.MatchString(text(k)) {
			end = k
			break
		}
	}
	if end >= 0 {
		start := end
		for start > 0 && numbered.MatchString(text(start-1)) {
			start--
		}
		if q := askQuestion(lines, clean[start]); q != "" {
			question = q
		}
	}
	if question == "" {
		return nil
	}
	return &askCard{Question: question, Options: out}
}

func circleAsk(screen string) *askCard {
	lines := strings.Split(screen, "\n")
	var clean []int
	for i, raw := range lines {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(raw, ""))
		if t == "" || strings.Trim(t, "─━╭╮╰╯┌┐└┘│┃ ") == "" || boxRule.MatchString(t) || (!circleOption.MatchString(t) && (hintLine.MatchString(t) || askHint.MatchString(t))) {
			continue
		}
		clean = append(clean, i)
	}
	text := func(k int) string { return strings.TrimSpace(boxEdge.ReplaceAllString(lines[clean[k]], "")) }
	end := len(clean) - 1
	for end >= 0 && circleOption.FindStringSubmatch(text(end)) == nil {
		end--
	}
	if end < 0 {
		return nil
	}
	start := end
	for start > 0 && circleOption.FindStringSubmatch(text(start-1)) != nil {
		start--
	}
	focus := -1
	out := make([]askChoice, 0, end-start+1)
	for k := start; k <= end; k++ {
		t := text(k)
		m := circleOption.FindStringSubmatch(t)
		if circleFocus.MatchString(t) {
			focus = k - start
		}
		out = append(out, askChoice{Label: strings.TrimSpace(m[2])})
	}
	if focus < 0 {
		return nil
	}
	for i := range out {
		out[i].Field, out[i].Value = "keys", circleKeys(focus, i)
	}
	question := askQuestion(lines, clean[start])
	if question == "" {
		return nil
	}
	return &askCard{Question: question, Options: out}
}

func circleKeys(focus, row int) string {
	n, step := row-focus, "down"
	if n < 0 {
		n, step = -n, "up"
	}
	if n == 0 {
		return "enter"
	}
	return strings.TrimSpace(strings.Repeat(step+" ", n) + "enter")
}

func askQuestion(lines []string, stop int) string {
	var body []string
	for _, raw := range lines[:stop] {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(raw, ""))
		if t == "" || strings.Trim(t, "─━╭╮╰╯┌┐└┘│┃ ") == "" || boxRule.MatchString(t) || hintLine.MatchString(t) || askHint.MatchString(t) || (!strings.HasSuffix(t, "?") && dialogFooter.MatchString(t)) {
			continue
		}
		body = append(body, t)
	}
	for i := len(body) - 1; i >= 0; i-- {
		if strings.HasSuffix(body[i], "?") {
			return body[i]
		}
	}
	if len(body) > 0 {
		return body[len(body)-1]
	}
	return ""
}

func ask(screen, status string) *askCard {
	if status == "blocked" {
		if card := numberedAsk(blockedPrompt(screen), screen); card != nil {
			return card
		}
		if card := circleAsk(screen); card != nil {
			return card
		}
	}
	return dialogAsk(screen)
}

func dialogAsk(screen string) *askCard {
	lines := strings.Split(screen, "\n")
	var clean []int
	for i, raw := range lines {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(raw, ""))
		keep := numbered.MatchString(t) || circleOption.MatchString(t) || strings.HasSuffix(t, "?")
		if t == "" || strings.Trim(t, "─━╭╮╰╯┌┐└┘│┃ ") == "" || boxRule.MatchString(t) || (!keep && (hintLine.MatchString(t) || askHint.MatchString(t) || dialogFooter.MatchString(t))) {
			continue
		}
		clean = append(clean, i)
	}
	if len(clean) < 2 {
		return nil
	}
	text := func(k int) string { return strings.TrimSpace(boxEdge.ReplaceAllString(lines[clean[k]], "")) }
	option := func(k int) bool { return numbered.MatchString(text(k)) || circleOption.MatchString(text(k)) }
	end := len(clean) - 1
	for end >= 0 && !option(end) {
		end--
	}
	if end < 1 {
		return nil
	}
	start := end
	for start > 0 && option(start-1) {
		start--
	}
	if start < 1 || !strings.HasSuffix(text(start-1), "?") || !askFooter(lines, clean[end]) {
		return nil
	}
	var out []askChoice
	switch {
	case numbered.MatchString(text(start)):
		for k := start; k <= end; k++ {
			m := numbered.FindStringSubmatch(text(k))
			if m == nil || m[1] != strconv.Itoa(k-start+1) || !slices.Contains(state.SupervisorKeys, m[1]) {
				return nil
			}
			out = append(out, askChoice{Label: m[2], Field: "key", Value: m[1]})
		}
	case circleOption.MatchString(text(start)):
		focus := -1
		for k := start; k <= end; k++ {
			t := text(k)
			m := circleOption.FindStringSubmatch(t)
			if m == nil {
				return nil
			}
			if circleFocus.MatchString(t) {
				focus = k - start
			}
			out = append(out, askChoice{Label: strings.TrimSpace(m[2])})
		}
		if focus < 0 {
			return nil
		}
		for i := range out {
			out[i].Field, out[i].Value = "keys", circleKeys(focus, i)
		}
	default:
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return &askCard{Question: text(start - 1), Options: out}
}

func askFooter(lines []string, end int) bool {
	seen := false
	for _, raw := range lines[end+1:] {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(raw, ""))
		switch {
		case t == "" || strings.Trim(t, "─━╭╮╰╯┌┐└┘│┃ ") == "":
			continue
		case askHint.MatchString(t) || dialogFooter.MatchString(t):
			seen = true
		default:
			return false
		}
	}
	return seen
}

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
	data["Page"], data["Fleets"] = "fleet", b.fleets(r.Context())
	b.render(w, http.StatusOK, "index.html", data)
}

func (b *Board) stripData(ctx context.Context) *statusView {
	sv, err := b.buildSupervisor(ctx, url.Values{})
	if err != nil {
		return nil
	}
	v := b.buildStatus(ctx, sv)
	return &v
}

func (b *Board) fleetData(ctx context.Context, q url.Values) (map[string]any, error) {
	device := ""
	if s, ok := ctx.Value(sessionKey{}).(session); ok && s.Device != nil {
		device = "phone"
	}
	f, err := b.facts(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		return nil, err
	}
	sv, err := b.buildSupervisor(ctx, q)
	if err != nil {
		return nil, err
	}
	c := viewContext{
		Controls: b.o.Controls,
		Base:     b.o.Base,
		Token:    b.csrf(ctx),
		Session:  device,
		Multi:    len(projects) > 1,
		Titles:   b.refTitle(ctx),
		Local:    b.local(),
	}
	needs, err := b.buildNeeds(ctx, f, sv, c)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Title": "board", "All": q.Get("all") == "1", "Controls": b.o.Controls,
		"Token": c.Token, "Live": true, "Base": c.Base, "Titles": c.Titles, "Local": c.Local,
		"Multi": c.Multi, "Console": buildConsole(sv, c), "Needs": needs,
	}
	if device != "" {
		data["Session"] = device
	}
	tl, err := b.buildTimeline(ctx, q, sv, c, true)
	if err != nil {
		return nil, err
	}
	data["Timeline"] = tl
	status := b.buildStatus(ctx, sv)
	status.Worst, status.WorstText, status.WorstWord, status.WorstCount, status.Waiting = needs.Worst, needs.WorstText, needs.WorstWord, needs.WorstCount, needs.Waiting
	status.NeedsHref = "#needs"
	data["Status"] = status
	tv, err := b.buildTasks(ctx, q, f, c, true)
	if err != nil {
		return nil, err
	}
	data["Tasks"] = tv
	return data, nil
}

func (b *Board) history(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := viewContext{Controls: b.o.Controls, Base: b.o.Base, Titles: b.refTitle(ctx), Local: b.local()}
	tl, err := b.buildTimeline(ctx, r.URL.Query(), supervisorView{}, c, false)
	if err != nil {
		b.failErr(w, err)
		return
	}
	data := map[string]any{"Title": "conversation", "Page": "log", "Fleets": b.fleets(ctx), "Timeline": tl}
	data["StripData"] = b.stripData(ctx)
	b.render(w, http.StatusOK, "log.html", data)
}

type screenView struct {
	supervisorView
	Controls     bool
	Base         string
	Token        string
	Pressed      int64
	Ask          *askCard
	NoSupervisor bool
}

func (b *Board) buildScreen(ctx context.Context, sv supervisorView, c viewContext) (screenView, error) {
	v := screenView{supervisorView: sv, Controls: c.Controls, Base: c.Base, Token: c.Token}
	switch {
	case sv.Sup == nil:
		v.NoSupervisor = true
		return v, nil
	case sv.Sup.Status != state.AttemptRunning:
		v.ScreenNote = state.SupervisorRef(sv.Sup.ID) + " is " + sv.Sup.Status + "; there is no live screen."
		return v, nil
	case sv.Stale != "":
		v.ScreenNote = sv.Stale
		return v, nil
	case sv.ScreenNote != "":
		return v, nil
	case sv.Screen == "":
		return v, nil
	}
	pressed, err := b.st.LastKeys(ctx, state.SupervisorRef(sv.Sup.ID))
	if err != nil {
		return v, err
	}
	v.Pressed, v.Ask = pressed, ask(sv.Screen, sv.Agent)
	return v, nil
}

func (b *Board) supervisorScreenData(ctx context.Context, q url.Values) (map[string]any, error) {
	f, err := b.facts(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		return nil, err
	}
	sv, err := b.buildSupervisor(ctx, q)
	if err != nil {
		return nil, err
	}
	c := viewContext{
		Controls: b.o.Controls,
		Base:     b.o.Base,
		Token:    b.csrf(ctx),
		Multi:    len(projects) > 1,
		Titles:   b.refTitle(ctx),
		Local:    b.local(),
	}
	needs, err := b.buildNeeds(ctx, f, sv, c)
	if err != nil {
		return nil, err
	}
	status := b.buildStatus(ctx, sv)
	status.Worst, status.WorstText, status.WorstWord, status.WorstCount, status.Waiting = needs.Worst, needs.WorstText, needs.WorstWord, needs.WorstCount, needs.Waiting
	status.NeedsHref = c.Base + "/#needs"
	screen, err := b.buildScreen(ctx, sv, c)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Title": "supervisor", "Page": "supervisor", "Controls": c.Controls, "Token": c.Token,
		"Live": true, "Base": c.Base, "Titles": c.Titles, "Local": c.Local, "Multi": c.Multi,
		"Status": status, "Screen": screen, "Needs": needs,
	}
	if s, ok := ctx.Value(sessionKey{}).(session); ok && s.Device != nil {
		data["Session"] = "phone"
	}
	return data, nil
}

func (b *Board) supervisorPage(w http.ResponseWriter, r *http.Request) {
	data, err := b.supervisorScreenData(r.Context(), url.Values{})
	if err != nil {
		b.failErr(w, err)
		return
	}
	data["Fleets"] = b.fleets(r.Context())
	b.render(w, http.StatusOK, "supervisor.html", data)
}

func lineWord(label string) string {
	switch label {
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

func pill(sup state.Supervisor, live paneState, held bool) (string, string) {
	switch {
	case sup.Status == state.AttemptRunning && live.Stale != "":
		return "failing", "unreachable"
	case live.Blocked:
		return "failing", "blocked"
	case held:
		return "failing", "held"
	case sup.Status == state.AttemptLaunching:
		return "neutral", "launching"
	case sup.Status == state.AttemptRunning && live.Agent == "working":
		return "working", "working"
	case sup.Status == state.AttemptRunning:
		return "ready", "ready"
	case sup.Status == state.AttemptStopped:
		return "neutral", sup.Status
	}
	return "failing", sup.Status
}

func hint(sup state.Supervisor, live paneState, held bool) (string, string) {
	ref := state.SupervisorRef(sup.ID)
	agent := "ready"
	if live.Agent == "working" {
		agent = "working"
	}
	switch {
	case sup.Status == state.AttemptLaunching:
		return "none", ref + " is starting; your message waits."
	case !sup.Live() && sup.Session != "":
		return "none", once(ref, "stopped", sup.Reason) + "; your message waits."
	case sup.Status != state.AttemptRunning:
		return "none", "No supervisor is running; your message waits."
	case live.Stale != "":
		return "none", "Luvus restarted; your message waits."
	case live.Blocked:
		return "blocked", ref + " is waiting on a screen in Needs; your message waits too."
	case sup.Switching():
		return agent, ref + " switches to " + sup.SwitchText() + " after this turn; your message waits."
	case held:
		return "blocked", ref + " did not answer the resume note; your message waits. Switch to another model from the model menu."
	}
	return agent, ""
}

type working struct {
	Ref, Since, Label, On, From, FromLabel, Last, LastLabel string
	Input                                                   int64
}

func (b *Board) working(ctx context.Context, sup state.Supervisor) (working, error) {
	w := working{Ref: state.SupervisorRef(sup.ID), Since: sup.CreatedAt, On: "since the turn began"}
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

type modelView struct {
	Name, Text, Efforts string
	Unverified          bool
}

type modelGroup struct {
	Label  string
	Models []modelView
}

type harnessChoice struct {
	Name    string
	Groups  []modelGroup
	Efforts []string
	Note    string
}

func (b *Board) choices(except string) []harnessChoice {
	var out []harnessChoice
	for _, h := range state.Harnesses {
		if h != except && harness.CheckSupervisor(harness.Spec{Harness: h}) == nil {
			out = append(out, b.picker(h))
		}
	}
	return out
}

var modelSources = []harness.Source{harness.SourceBuiltin, harness.SourceDiscovered, harness.SourceCustom}

func (b *Board) picker(name string) harnessChoice {
	c := harnessChoice{Name: name}
	models, err := harness.Detected(name, b.o.Harness)
	if err == nil && len(models) == 0 {
		return c
	}
	now := b.now()
	c.Note = modelNote(harness.StatusOf(name, b.o.Harness, now), now)
	for _, src := range modelSources {
		g := modelGroup{Label: string(src)}
		unverified := false
		for _, m := range models {
			if m.Source != src {
				continue
			}
			unverified = unverified || m.Unverified
			g.Models = append(g.Models, modelView{Name: m.Name, Text: modelText(m), Efforts: strings.Join(m.Efforts, " "), Unverified: m.Unverified})
			for _, e := range m.Efforts {
				if !slices.Contains(c.Efforts, e) {
					c.Efforts = append(c.Efforts, e)
				}
			}
		}
		if unverified {
			g.Label += ", efforts unverified"
		}
		if len(g.Models) > 0 {
			c.Groups = append(c.Groups, g)
		}
	}
	return c
}

func modelText(m harness.Model) string {
	if m.Label == "" || m.Label == m.Name {
		return m.Name
	}
	return m.Label + " · " + m.Name
}

func modelNote(st harness.Status, now time.Time) string {
	var parts []string
	if st.Old {
		parts = append(parts, "listed "+listedAge(now.Sub(st.Listing.At))+" ago")
	}
	if f := st.Listing.Failed; f != nil {
		parts = append(parts, "refresh failed: "+f.Reason)
	}
	return strings.Join(parts, "; ")
}

func listedAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", max(1, int(d/time.Minute)))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d d", int(d/(24*time.Hour)))
}

const batchReason = "found only as a batch file"

type profile struct{ Name, Label, Missing, Gone, Warn string }

func (b *Board) profiles() []profile { return b.profilesFor("") }

func (b *Board) profilesFor(name string) []profile {
	p, err := harness.LoadPolicy(b.o.Home)
	if err != nil {
		return nil
	}
	missing := map[string]string{}
	type listing struct {
		st harness.Status
		ok bool
	}
	listed := map[string]listing{}
	now := b.now()
	var out []profile
	for _, n := range p.Names() {
		s := p.Profiles[n].Spec
		if (name != "" && s.Harness != name) || p.Profiles[n].Approve == state.ApproveAsk || p.Profiles[n].Access == state.AccessReadonly || p.Profiles[n].MCP != "" || harness.CheckSupervisor(s) != nil {
			continue
		}
		reason, seen := missing[s.Harness]
		if !seen {
			var batch *harness.BatchFileError
			switch _, err := harness.Find(s.Harness, b.o.Harness); {
			case errors.As(err, &batch):
				reason = s.Harness + " " + batchReason
			case err != nil:
				reason = s.Harness + " not found on the board's PATH"
			}
			missing[s.Harness] = reason
		}
		p := profile{Name: n, Label: strings.Join(strings.Fields(n+" · "+s.Harness+" "+s.Model+" "+s.Effort), " "), Missing: reason}
		if reason == "" {
			l, seen := listed[s.Harness]
			if !seen {
				_, err := harness.Detected(s.Harness, b.o.Harness)
				l = listing{harness.StatusOf(s.Harness, b.o.Harness, now), err == nil}
				listed[s.Harness] = l
			}
			if why, hard := l.st.Vanished(s); hard {
				p.Gone = why
			} else if warn, err := harness.Check(s, b.o.Harness); err == nil && l.ok {
				p.Warn = warn
			}
		}
		out = append(out, p)
	}
	return out
}

func (p profile) Off() bool { return p.Missing != "" || p.Gone != "" }

func chosenProfile(ps []profile) string {
	first := ""
	for _, p := range ps {
		if p.Off() {
			continue
		}
		if p.Name == "default" {
			return p.Name
		}
		if first == "" {
			first = p.Name
		}
	}
	if !slices.ContainsFunc(ps, func(p profile) bool { return p.Name == "default" }) {
		return ""
	}
	return first
}

func noneStartable(ps []profile) string {
	if len(ps) == 0 || slices.ContainsFunc(ps, func(p profile) bool { return !p.Off() }) {
		return ""
	}
	var gone, batch, left []string
	for _, p := range ps {
		if p.Missing == "" {
			if !slices.Contains(left, p.Gone) {
				left = append(left, p.Gone)
			}
			continue
		}
		h, _, _ := strings.Cut(p.Missing, " ")
		list := &gone
		if strings.HasSuffix(p.Missing, batchReason) {
			list = &batch
		}
		if !slices.Contains(*list, h) {
			*list = append(*list, h)
		}
	}
	var parts []string
	if len(gone) > 0 {
		parts = append(parts, strings.Join(gone, ", ")+" not found on the board's PATH. Install the harness or add its folder to PATH")
	}
	if len(batch) > 0 {
		parts = append(parts, strings.Join(batch, ", ")+" "+batchReason+", which Hand does not start. Install the standalone harness or add the folder of its .exe to PATH")
	}
	text := ""
	if len(parts) > 0 {
		text = strings.Join(parts, ". ") + ", then restart the board: `hand board stop`, then `hand board`, or restart its unit."
	}
	if len(left) > 0 {
		text = strings.TrimSpace(text + " " + strings.Join(left, ". ") + ".")
	}
	return text
}

var launched = regexp.MustCompile(`^supervisor s[0-9]+ started$`)

const deliveredLookback = 50

var lifecycleKinds = []string{"supervisor.keys", "supervisor.started", "supervisor.stopped", "supervisor.exited", "supervisor.interrupted", "supervisor.failed", "supervisor.fresh", "supervisor.switch", "supervisor.blocked", "supervisor.refused", "luvus.switched", "luvus.failed"}

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
		case "luvus.switched":
			ref, text = "", "Luvus "+strings.Replace(e.Detail, " -> ", " → ", 1)+" (switched while quiet)"
		case "luvus.failed":
			ref, text = "", "Luvus switch to "+ref+" failed: "+detail
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
		out = append(out, chatItem{Entry: transcript.Entry{Role: "hand", Text: text, At: e.At}, Ref: ref, Kind: e.Kind})
	}
	return out, nil
}

func (c chatItem) When() string {
	if c.Clock {
		return hm(c.At)
	}
	return when(c.At)
}

type chatItem struct {
	transcript.Entry
	No                  int
	Ref, Model, Day     string
	Kind                string
	Delivered, Delivery string
	Clock               bool
	Input               int64
	Wakes               []chatItem
	Shots               []attachment
}

func once(ref, kind, detail string) string {
	if detail == "" {
		return ref + " " + kind
	}
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
		for j < len(all) && wake(all[j]) && (j == i || parse(all[j].At).UTC().Format(time.DateOnly) == parse(all[i].At).UTC().Format(time.DateOnly)) {
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
		return nil, "No supervisor", nil, err
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
		return nil, "No supervisor"
	case b.o.Transcript == nil:
		return nil, transcript.ErrUnreadable.Error()
	}
	dir := b.o.Home
	if sup.Harness == "omp" {
		getenv := b.o.Harness.Getenv
		if getenv == nil {
			getenv = func(string) string { return "" }
		}
		dir = harness.OmpAgent(getenv, b.o.Home)
	}
	entries, err := b.o.Transcript.Read(ctx, sup.Harness, sup.Session, dir)
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
	warn, err := b.o.Control(r.Context(), args...)
	if err != nil {
		b.failErr(w, err)
		return
	}
	if warn != "" {
		w.Header().Set("X-Hand-Warn", url.PathEscape(warn))
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

func modelEffortArgs(r *http.Request) []string {
	var args []string
	model := strings.TrimSpace(r.PostFormValue("other"))
	if model == "" {
		model = strings.TrimSpace(r.PostFormValue("model"))
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if v := strings.TrimSpace(r.PostFormValue("effort")); v != "" {
		args = append(args, "--effort", v)
	}
	return args
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
		args = append(args, modelEffortArgs(r)...)
	}
	b.run(w, r, "Starting the supervisor…", args...)
}

func (b *Board) keys(w http.ResponseWriter, r *http.Request) {
	b.press(w, r, "supervisor", "", b.o.Base+"/supervisor")
}

func (b *Board) attemptKeys(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if n, err := strconv.ParseInt(strings.TrimPrefix(ref, "a"), 10, 64); err != nil || n < 1 || !strings.HasPrefix(ref, "a") {
		b.fail(w, http.StatusBadRequest, "press keys on an attempt shown by this page")
		return
	}
	b.press(w, r, "attempt", ref, b.o.Base+"/")
}

func (b *Board) press(w http.ResponseWriter, r *http.Request, group, target, next string) {
	if !b.allowed(w) {
		return
	}
	keys := strings.Fields(strings.Join(r.PostForm["keys"], " "))
	if len(keys) == 0 {
		keys = strings.Fields(strings.Join(r.PostForm["key"], " "))
	}
	rev := r.PostFormValue("revision")
	ok := len(keys) > 0
	if n, err := strconv.ParseInt(rev, 10, 64); err != nil || n < 0 {
		ok = false
	}
	for _, k := range keys {
		if !slices.Contains(state.SupervisorKeys, k) && !(group == "attempt" && len(k) == 1 && k[0] >= '4' && k[0] <= '9') {
			ok = false
		}
	}
	if !ok {
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
	if _, err := b.o.Control(r.Context(), append(args, keys...)...); err != nil {
		if errors.Is(err, state.ErrConflict) {
			b.fail(w, http.StatusConflict, staleScreen)
			return
		}
		b.failErr(w, err)
		return
	}
	b.done(w, r, next, "Sent "+strings.Join(keys, " ")+" to "+to)
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
		if v := strings.TrimSpace(r.PostFormValue("harness")); v != "" {
			args = append(args, "--harness", v)
		}
		args = append(args, modelEffortArgs(r)...)
	}
	if len(args) == 2 {
		b.fail(w, http.StatusBadRequest, "pick a profile or give a model or effort")
		return
	}
	warn, err := b.o.Control(r.Context(), args...)
	if err != nil {
		b.failErr(w, err)
		return
	}
	receipt := "Switch applied"
	sup, ok, _ := b.st.LatestSupervisor(r.Context())
	switch h := strings.TrimSpace(r.PostFormValue("harness")); {
	case r.PostFormValue("cancel") == "1":
		receipt = "Switch canceled"
	case h != "":
		receipt = "Switching to " + h + "…"
	case ok && sup.Switching():
		receipt = "Switch set for after this turn"
	case strings.Contains(warn, state.NoteLost):
		receipt = "Switch applied, not answered"
	}
	if warn != "" {
		w.Header().Set("X-Hand-Warn", url.PathEscape(warn))
	}
	b.done(w, r, b.o.Base+"/", receipt)
}
