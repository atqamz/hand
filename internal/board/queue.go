package board

import (
	"context"
	"maps"
	"math"
	"net/url"
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

var severity = map[string]struct {
	rank       int
	tone, word string
}{
	"blocked":  {0, "fail", "BLOCKED"},
	"worker":   {1, "fail", "BLOCKED"},
	"failure":  {2, "fail", "FAILED"},
	"nosup":    {3, "wait", "NO SUPERVISOR"},
	"resume":   {4, "fail", "INTERRUPTED"},
	"decision": {5, "wait", "DECISION"},
	"report":   {6, "neutral", "REPORT"},
	"quiet":    {7, "neutral", "QUIET"},
}

func (w waiting) Rank() int { return severity[w.Kind].rank }

func (w waiting) Tone() string { return severity[w.Kind].tone }

func (w waiting) Word() string { return severity[w.Kind].word }

func (b *Board) queueData(ctx context.Context, data map[string]any, _ url.Values) error {
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return err
	}
	var waits []waiting
	total := 0
	add := func(w waiting) {
		total++
		w.Title = clip(w.Title, titleRunes)
		waits = append(waits, w)
	}
	sup, live := data["Sup"].(state.Supervisor)
	if blocked, _ := data["Blocked"].(bool); blocked && live {
		hint, _ := data["Hint"].(string)
		screen, _ := data["Screen"].(string)
		rev, _ := data["Revision"].(int64)
		dig, _ := data["Digest"].(string)
		title := hint
		if title == "" {
			title = blockedAsking
		}
		pressed, err := b.st.LastKeys(ctx, state.SupervisorRef(sup.ID))
		if err != nil {
			return err
		}
		w := waiting{Kind: "blocked", Ref: state.SupervisorRef(sup.ID), Title: title, Hint: hint, Screen: lastLines(screen, screenLines), Revision: rev, Digest: dig, Pressed: pressed, Sup: &sup, Prompt: blockedPrompt(screen)}
		if w.Prompt.Headline != "" {
			w.Title = w.Prompt.Headline
		}
		add(w)
	}
	supervised := live && sup.Status == state.AttemptRunning
	liveAttempts, err := b.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	signals, err := b.signals(ctx, liveAttempts)
	if err != nil {
		return err
	}
	workers, err := b.workers(ctx, liveAttempts, signals, supervised, true)
	if err != nil {
		return err
	}
	for _, w := range workers {
		add(w)
	}
	decisions, err := b.st.OpenDecisions(ctx, 0, maxWaits)
	if err != nil {
		return err
	}
	asked, err := b.st.OpenDecisionCount(ctx, 0)
	if err != nil {
		return err
	}
	tasks := map[int64]state.Task{}
	task := func(id int64) (state.Task, error) {
		if t, ok := tasks[id]; ok {
			return t, nil
		}
		t, err := b.st.Task(ctx, id)
		tasks[id] = t
		return t, err
	}
	for _, d := range decisions {
		t, err := task(d.TaskID)
		if err != nil {
			return err
		}
		add(waiting{Kind: "decision", Ref: state.DecisionRef(d.ID), Title: d.Headline(), Task: t, Decision: &d})
	}
	total += asked - len(decisions)
	f, _ := data["facts"].(facts)
	failures, err := b.failures(ctx, f)
	if err != nil {
		return err
	}
	for _, w := range failures {
		add(w)
	}
	unread, err := b.st.Reports(ctx, state.ReportFilter{Unacked: true, Oldest: true}, maxWaits)
	if err != nil {
		return err
	}
	unacked, err := b.st.UnackedReportCount(ctx)
	if err != nil {
		return err
	}
	for _, r := range unread {
		t, err := task(r.TaskID)
		if err != nil {
			return err
		}
		_, rest, _ := strings.Cut(strings.TrimSpace(r.Body), "\n")
		excerpt, cut := excerptOf(rest)
		add(waiting{Kind: "report", Ref: state.ReportRef(r.ID), Title: markers.Replace(r.Summary()), Task: t, Report: &r, Excerpt: excerpt, Cut: cut})
	}
	total += unacked - len(unread)
	resume := live && sup.Session != "" && (sup.Status == state.AttemptInterrupted || sup.Status == state.AttemptExited)
	if resume {
		add(waiting{Kind: "resume", Ref: state.SupervisorRef(sup.ID), Title: "The supervisor stopped unexpectedly", Sup: &sup})
	}
	if !supervised && !resume {
		pending, _ := data["Pending"].(int)
		waitsOnOne := len(liveAttempts) > 0 || pending > 0
		if !waitsOnOne {
			cursor := int64(0)
			if live {
				cursor = sup.WakeCursor
			}
			answered, err := b.st.EventsAfter(ctx, cursor, []string{"decision.answered"}, 1)
			if err != nil {
				return err
			}
			waitsOnOne = len(answered) > 0
		}
		if waitsOnOne {
			add(waiting{Kind: "nosup", Title: "Work is waiting for a supervisor"})
		}
	}
	data["Active"], data["Inbox"] = counts[state.StatusActive], counts[state.StatusInbox]
	if len(waits) == 0 {
		cleared, err := b.st.RecentEventsOf(ctx, []string{"decision.answered", "report.acked", "supervisor.keys", "attempt.keys"}, 1)
		if err != nil {
			return err
		}
		if len(cleared) > 0 {
			data["Cleared"] = cleared[0].At
		}
		var parts []string
		if n := len(liveAttempts); n > 0 {
			parts = append(parts, strconv.Itoa(n)+" running")
		}
		if n := counts[state.StatusInbox]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" inbox")
		}
		data["Summary"] = strings.Join(parts, " · ")
	}
	slices.SortStableFunc(waits, func(x, y waiting) int { return x.Rank() - y.Rank() })
	waits = waits[:min(len(waits), maxWaits)]
	data["Waits"], data["More"], data["Waiting"] = waits, total-len(waits), total
	data["Worst"], data["WorstText"], data["WorstWord"], data["WorstCount"] = "", "", "", total
	if len(waits) > 0 {
		data["Worst"], data["WorstText"], data["WorstWord"] = waits[0].Kind, strings.TrimSpace(waits[0].Ref+" "+waits[0].Title), "waiting"
		if waits[0].Tone() == "fail" {
			n := 0
			for _, w := range waits {
				if w.Word() == waits[0].Word() {
					n++
				}
			}
			data["WorstWord"], data["WorstCount"] = strings.ToLower(waits[0].Word()), n
		}
	}
	return nil
}

func (b *Board) signals(ctx context.Context, live []state.Attempt) (map[int64]state.Event, error) {
	out, err := b.st.AttemptSignals(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range live {
		if out[a.ID].Kind != "attempt.blocked" {
			continue
		}
		if ag, err := b.o.Luvus.Explain(ctx, a.PaneID); err == nil && ag.Status != "blocked" && ag.Status != "idle" {
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
		case e.Kind == "attempt.quiet" && strings.HasSuffix(e.Detail, "without a new report"):
			kind = "quiet"
		default:
			continue
		}
		if supervised && now.Sub(parse(e.At)) < workerGrace {
			continue
		}
		t, err := b.st.Task(ctx, a.TaskID)
		if err != nil {
			return nil, err
		}
		w := waiting{Kind: kind, Ref: state.AttemptRef(a.ID), Task: t, Attempt: &a, Title: "Its turn ended without a report"}
		if kind == "worker" {
			w.Title = strings.TrimPrefix(e.Detail, state.AttemptRef(a.ID)+": ")
			if w.Title == "" {
				w.Title = "Its screen needs a key"
			}
			if screens {
				if s, err := b.o.Luvus.Read(ctx, a.PaneID, luvus.ScreenLines); err == nil && s.TerminalID == a.TerminalID {
					w.Screen, w.Revision, w.Digest, w.Prompt = lastLines(s.Text, screenLines), s.ContentRevision, luvus.ScreenDigest(s.Text), blockedPrompt(s.Text)
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
	signals map[int64]state.Event
	latest  map[int64]state.Attempt
	done    map[int64]bool
	unread  map[int64]bool
	asked   map[int64]int
	prs     map[int64]string
}

func (b *Board) facts(ctx context.Context) (facts, error) {
	var f facts
	var err error
	if f.latest, err = b.st.LatestAttempts(ctx); err != nil {
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
	f.asked, err = b.st.OpenDecisionCounts(ctx)
	return f, err
}

func (b *Board) prLinks(ctx context.Context, tasks []state.Task) (map[int64]string, error) {
	ids := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	reports, err := b.st.ReportsMentioning(ctx, "/pull/", ids)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, r := range reports {
		if links := PRLinks(r.Body); len(links) > 0 {
			out[r.TaskID] = links[len(links)-1]
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
	Task    state.Task
	State   string
	Agent   string
	Attempt *state.Attempt
	PR      string
}

func (f facts) check(t state.Task) check {
	c := check{Task: t, State: "idle", PR: f.prs[t.ID]}
	failed, done := false, t.Status == state.StatusDone
	if a, ok := f.latest[t.ID]; ok {
		c.Attempt = &a
		failed = t.Status == state.StatusActive && f.failing(a)
		if a.Live() {
			switch f.signals[a.ID].Kind {
			case "attempt.blocked":
				c.Agent = "blocked"
			case "attempt.quiet":
				c.Agent = "quiet"
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
