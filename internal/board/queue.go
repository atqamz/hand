package board

import (
	"context"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const (
	maxWaits      = 50
	titleRunes    = 200
	excerptBytes  = 2000
	excerptLines  = 12
	screenLines   = 20
	blockedAsking = "The supervisor's screen needs a key"
	workerGrace   = 10 * time.Minute
)

var markers = strings.NewReplacer("**", "", "`", "")

type waiting struct {
	Kind, Ref, Title string
	Task             state.Task
	Decision         *state.Decision
	Report           *state.Report
	Attempt          *state.Attempt
	Screen           string
	Revision         int64
	Digest           string
	Pressed          int64
	Hint             string
	Sup              *state.Supervisor
	Excerpt          string
	Cut              bool
	Prompt           prompt
	Command          string
}

type prompt struct {
	Headline string
	Options  []key
	Deadline string
}

var (
	numbered = regexp.MustCompile(`^(?:[❯›]\s*)?([1-9])\.\s+(.+?)$`)
	autoDeny = regexp.MustCompile(`deny this request in (\d+:\d{2})`)
	boxRule  = regexp.MustCompile(`^\s*(?:[─━]{10,}\s*|╭.*)$`)
	boxEdge  = regexp.MustCompile(`^[\s│┃]+|[\s│┃]+$`)
	hintLine = regexp.MustCompile(`(?i)\besc to\b|\benter to\b|\btab to\b`)
	kindWord = regexp.MustCompile(`^\p{L}+`)
	refusal  = regexp.MustCompile(`^No\b`)
)

func blockedPrompt(screen string) prompt {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if boxRule.MatchString(lines[i]) {
			lines = lines[i+1:]
			break
		}
	}
	var p prompt
	var clean []string
	for _, l := range lines {
		t := boxEdge.ReplaceAllString(l, "")
		if m := autoDeny.FindStringSubmatch(t); m != nil {
			p.Deadline = m[1]
			continue
		}
		if t != "" && !hintLine.MatchString(t) {
			clean = append(clean, t)
		}
	}
	end := -1
	for i := len(clean) - 1; i >= 0 && end < 0; i-- {
		if numbered.MatchString(clean[i]) {
			end = i
		}
	}
	if end < 0 {
		return p
	}
	start := end
	for start > 0 && numbered.MatchString(clean[start-1]) {
		start--
	}
	for i := end; i >= start; i-- {
		if numbered.FindStringSubmatch(clean[i])[1] == "1" {
			start = i
			break
		}
	}
	body := clean[:start]
	q := -1
	for i, l := range body {
		if strings.HasSuffix(l, "?") {
			q = i
		}
	}
	if q < 0 {
		return p
	}
	var no, rest []key
	fits := true
	for i, l := range clean[start : end+1] {
		m := numbered.FindStringSubmatch(l)
		if m[1] != strconv.Itoa(i+1) || !slices.Contains(state.SupervisorKeys, m[1]) {
			fits = false
			break
		}
		k := key{m[1], m[1] + " " + clip(m[2], 24)}
		if refusal.MatchString(m[2]) {
			no = append(no, k)
		} else {
			rest = append(rest, k)
		}
	}
	if fits {
		p.Options = append(append(no, key{"esc", "Esc"}), rest...)
	}
	cmd, kind := "", ""
	switch {
	case q+1 < len(body):
		cmd = body[q+1]
	case q >= 2:
		cmd = body[1]
	}
	if q >= 1 {
		kind = kindWord.FindString(body[0])
	}
	switch {
	case kind != "" && cmd != "":
		p.Headline = kind + ": " + cmd + " · " + body[q]
	case cmd != "":
		p.Headline = cmd + " · " + body[q]
	case kind != "":
		p.Headline = kind + " · " + body[q]
	default:
		p.Headline = body[q]
	}
	return p
}

func waitPrompt(screen string) prompt {
	p := blockedPrompt(screen)
	if p.Headline != "" && len(p.Options) > 0 {
		return p
	}
	card := ask(screen, "blocked")
	if card == nil {
		return p
	}
	if p.Headline == "" {
		p.Headline = card.Question
	}
	if len(p.Options) == 0 && len(card.Options) > 0 {
		p.Options = make([]key, 0, len(card.Options))
		for _, o := range card.Options {
			p.Options = append(p.Options, key{o.Value, o.Label})
		}
	}
	return p
}

type severityRank struct {
	rank       int
	tone, word string
}

var severity = map[string]severityRank{
	"blocked":   {0, "fail", "BLOCKED"},
	"worker":    {1, "fail", "BLOCKED"},
	"failure":   {2, "fail", "FAILED"},
	"limited":   {3, "wait", "LIMITED"},
	"nosup":     {4, "wait", "NO SUPERVISOR"},
	"stalled":   {4, "wait", "STALLED"},
	"resume":    {5, "fail", "INTERRUPTED"},
	"decision":  {6, "wait", "DECISION"},
	"report":    {7, "neutral", "REPORT"},
	"quiet":     {8, "neutral", "QUIET"},
	"long":      {9, "neutral", "LONG TURN"},
	"kept":      {10, "neutral", "KEPT"},
	"artifacts": {11, "neutral", "OLD FILES"},
}

func (w waiting) Rank() int { return severity[w.Kind].rank }

func (w waiting) Tone() string { return severity[w.Kind].tone }

func (w waiting) Word() string { return severity[w.Kind].word }

func (b *Board) signals(ctx context.Context, live []state.Attempt) (map[int64]state.Event, error) {
	out, err := b.st.AttemptSignals(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range live {
		kind := out[a.ID].Kind
		if kind != "attempt.blocked" && kind != "attempt.limited" && kind != "attempt.long" && kind != "attempt.quiet" && kind != "attempt.idle" {
			continue
		}
		ag, err := b.o.Luvus.Explain(ctx, a.PaneID)
		if err != nil {
			continue
		}
		if (kind == "attempt.blocked" && ag.Status != "blocked" && ag.Status != "idle") || (kind == "attempt.limited" && ag.Status == "working") || (kind == "attempt.long" && ag.Status != "working") || ((kind == "attempt.quiet" || kind == "attempt.idle") && ag.Status == "working") {
			delete(out, a.ID)
		}
	}
	return out, nil
}

func (b *Board) workers(ctx context.Context, live []state.Attempt, signals map[int64]state.Event, supervised, screens bool) ([]waiting, error) {
	now := b.now()
	var out []waiting
	for _, a := range live {
		e, ok := signals[a.ID]
		if !ok || a.Status != state.AttemptRunning {
			continue
		}
		kind := ""
		switch {
		case e.Kind == "attempt.blocked":
			kind = "worker"
		case (e.Kind == "attempt.quiet" || e.Kind == "attempt.idle") && (strings.HasSuffix(e.Detail, "without a new report") || strings.Contains(e.Detail, ": api error: ")):
			kind = "quiet"
		case e.Kind == "attempt.limited":
			kind = "limited"
		case e.Kind == "attempt.long":
			kind = "long"
		default:
			continue
		}
		if supervised && kind != "limited" && kind != "long" && now.Sub(parse(e.At)) < workerGrace {
			continue
		}
		t, err := b.st.Task(ctx, a.TaskID)
		if err != nil {
			return nil, err
		}
		w := waiting{Kind: kind, Ref: state.AttemptRef(a.ID), Task: t, Attempt: &a, Title: "Its turn ended without a report"}
		if kind != "quiet" {
			w.Title = strings.TrimPrefix(e.Detail, state.AttemptRef(a.ID)+": ")
		}
		if kind == "worker" {
			if w.Title == "" {
				w.Title = "Its screen needs a key"
			}
			if screens {
				if s, err := b.o.Luvus.Read(ctx, a.PaneID, luvus.ScreenLines); err == nil && s.TerminalID == a.TerminalID {
					w.Screen, w.Revision, w.Digest, w.Prompt = lastLines(s.Text, screenLines), s.ContentRevision, luvus.ScreenDigest(s.Text), waitPrompt(s.Text)
					if w.Prompt.Headline != "" {
						w.Title = w.Prompt.Headline
					}
					if w.Pressed, err = b.st.LastKeys(ctx, w.Ref); err != nil {
						return nil, err
					}
				}
			}
		}
		out = append(out, w)
	}
	quiet := func(w waiting) int {
		if w.Kind == "quiet" {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(out, func(x, y waiting) int { return quiet(x) - quiet(y) })
	return out, nil
}

type facts struct {
	signals   map[int64]state.Event
	latest    map[int64]state.Attempt
	done      map[int64]bool
	unread    map[int64]bool
	asked     map[int64]int
	waits     map[int64]state.Timer
	delivered map[int64]Deliverable
}

func (b *Board) facts(ctx context.Context) (facts, error) {
	var f facts
	var err error
	if f.latest, err = b.st.LatestAttemptsBriefless(ctx); err != nil {
		return f, err
	}
	if f.signals, err = b.signals(ctx, slices.Collect(maps.Values(f.latest))); err != nil {
		return f, err
	}
	if f.done, err = b.st.DoneReportAttempts(ctx); err != nil {
		return f, err
	}
	if f.unread, err = b.st.UnackedReportTasks(ctx); err != nil {
		return f, err
	}
	if f.asked, err = b.st.OpenDecisionCounts(ctx); err != nil {
		return f, err
	}
	f.waits, err = b.st.Waits(ctx)
	return f, err
}

func (b *Board) deliverables(ctx context.Context, tasks []state.Task) (map[int64]Deliverable, error) {
	ids := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	reports, err := b.st.ReportsMentioning(ctx, []string{"Delivered:", "/pull", "/merge_requests/", "/pull-requests/"}, ids)
	if err != nil {
		return nil, err
	}
	events, err := b.st.DeliveredEvents(ctx, ids...)
	if err != nil {
		return nil, err
	}
	byReport, byEvent := map[int64][]state.Report{}, map[int64][]state.Event{}
	for _, r := range reports {
		byReport[r.TaskID] = append(byReport[r.TaskID], r)
	}
	for _, e := range events {
		byEvent[e.TaskID] = append(byEvent[e.TaskID], e)
	}
	out := map[int64]Deliverable{}
	for _, id := range ids {
		if found := Newest(byReport[id], byEvent[id]); len(found) > 0 {
			out[id] = b.linked(found[:1])[0]
		}
	}
	return out, nil
}

func (f facts) failing(a state.Attempt) bool {
	switch a.Status {
	case state.AttemptFailed, state.AttemptInterrupted:
		return true
	case state.AttemptExited:
		return !f.done[a.ID]
	}
	return false
}

func (b *Board) failures(ctx context.Context, f facts) ([]waiting, error) {
	tasks, err := b.st.Tasks(ctx, []string{state.StatusActive}, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	var out []waiting
	for _, t := range tasks {
		if _, waiting := f.waits[t.ID]; waiting {
			continue
		}
		if a, ok := f.latest[t.ID]; ok && f.failing(a) {
			out = append(out, waiting{Kind: "failure", Ref: state.AttemptRef(a.ID), Title: t.Title, Task: t, Attempt: &a})
		}
	}
	slices.SortFunc(out, func(x, y waiting) int { return int(x.Attempt.ID - y.Attempt.ID) })
	return out, nil
}

func excerptOf(body string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	cut := len(lines) > excerptLines
	out := strings.Join(lines[:min(len(lines), excerptLines)], "\n")
	if len(out) > excerptBytes {
		out, cut = strings.ToValidUTF8(out[:excerptBytes], "")+"…", true
	}
	return out, cut
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

func when(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return stamp
	}
	return t.UTC().Format("Jan 2 15:04 UTC")
}

type check struct {
	Task       state.Task
	State      string
	Agent      string
	Attempt    *state.Attempt
	FinishedAt string
	Delivered  *Deliverable
	Waits      *state.Timer
	Depth      int
	Under      int64
	Subs       int
}

func (c check) Indent() int { return min(c.Depth, 3) }

func (f facts) check(t state.Task) check {
	c := check{Task: t, State: "idle", FinishedAt: t.FinishedAt}
	if d, ok := f.delivered[t.ID]; ok {
		c.Delivered = &d
	}
	failed, done := false, t.Status == state.StatusDone
	w, waiting := f.waits[t.ID]
	if waiting {
		c.Waits = &w
	}
	if a, ok := f.latest[t.ID]; ok {
		c.Attempt = &a
		failed = t.Status == state.StatusActive && f.failing(a) && !waiting
		if a.Live() {
			switch f.signals[a.ID].Kind {
			case "attempt.blocked":
				c.Agent = "blocked"
			case "attempt.quiet", "attempt.idle":
				c.Agent = "quiet"
			case "attempt.limited":
				c.Agent = "limited"
			}
		}
		done = done || f.done[a.ID]
	}
	switch {
	case failed:
		c.State = "failing"
	case f.asked[t.ID] > 0 || f.unread[t.ID]:
		c.State = "waiting"
	case c.Attempt != nil && c.Attempt.Live():
		c.State = "running"
	case done:
		c.State = "passing"
	}
	return c
}
