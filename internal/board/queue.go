package board

import (
	"context"
	"maps"
	"math"
	"net/url"
	"slices"
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
}

var wireCodes = map[string][2]string{
	"blocked":  {"FLASH", "BLOCKED"},
	"decision": {"BULLETIN", "DECISION"},
	"failure":  {"URGENT", "FAILED"},
	"report":   {"ROUTINE", "REPORT"},
	"resume":   {"SERVICE", "RESUME"},
	"worker":   {"BLOCKED", "WORKER"},
	"quiet":    {"QUIET", "WORKER"},
	"nosup":    {"STOPPED", "SUPERVISOR"},
}

func (w waiting) Code() string { return wireCodes[w.Kind][0] }

func (w waiting) Word() string { return wireCodes[w.Kind][1] }

func (b *Board) queueData(ctx context.Context, data map[string]any, _ url.Values) error {
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return err
	}
	var waits []waiting
	total := 0
	add := func(w waiting) bool {
		total++
		if len(waits) >= maxWaits {
			return false
		}
		w.Title = clip(w.Title, titleRunes)
		waits = append(waits, w)
		return true
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
		add(waiting{Kind: "blocked", Ref: state.SupervisorRef(sup.ID), Title: title, Hint: hint, Screen: lastLines(screen, screenLines), Revision: rev, Digest: dig, Pressed: pressed, Sup: &sup})
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
	unread, err := b.st.Reports(ctx, state.ReportFilter{Unacked: true}, maxWaits)
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
			add(waiting{Kind: "nosup", Title: "No supervisor is running"})
		}
	}
	data["Active"], data["Inbox"] = counts[state.StatusActive], counts[state.StatusInbox]
	data["Waits"], data["More"], data["Waiting"] = waits, total-len(waits), total
	data["Worst"], data["WorstText"] = "", ""
	if len(waits) > 0 {
		data["Worst"], data["WorstText"] = waits[0].Kind, strings.TrimSpace(waits[0].Ref+" "+waits[0].Title)
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
		if ag, err := b.o.Luvus.Explain(ctx, a.PaneID); err == nil && ag.Status != "blocked" {
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
			if s, err := b.o.Luvus.Read(ctx, a.PaneID, luvus.ScreenLines); screens && err == nil && s.TerminalID == a.TerminalID {
				w.Screen, w.Revision, w.Digest = lastLines(s.Text, screenLines), s.ContentRevision, luvus.ScreenDigest(s.Text)
				if w.Pressed, err = b.st.LastKeys(ctx, w.Ref); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, w)
	}
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
