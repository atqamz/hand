package board

import (
	"context"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const (
	maxWaits      = 50
	titleRunes    = 200
	excerptBytes  = 2000
	excerptLines  = 12
	screenLines   = 20
	blockedAsking = "The supervisor's screen needs a key"
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
		title := hint
		if title == "" {
			title = blockedAsking
		}
		add(waiting{Kind: "blocked", Ref: state.SupervisorRef(sup.ID), Title: title, Hint: hint, Screen: lastLines(screen, screenLines), Revision: rev, Sup: &sup})
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
		add(waiting{Kind: "decision", Ref: state.DecisionRef(d.ID), Title: d.Question, Task: t, Decision: &d})
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
		excerpt, cut := excerptOf(r.Body)
		add(waiting{Kind: "report", Ref: state.ReportRef(r.ID), Title: markers.Replace(r.Summary()), Task: t, Report: &r, Excerpt: excerpt, Cut: cut})
	}
	total += unacked - len(unread)
	if live && sup.Session != "" && (sup.Status == state.AttemptInterrupted || sup.Status == state.AttemptExited) {
		if add(waiting{Kind: "resume", Ref: state.SupervisorRef(sup.ID), Title: "The supervisor stopped unexpectedly", Sup: &sup}) {
			data["Resumable"] = false
		}
	}
	data["Active"], data["Inbox"] = counts[state.StatusActive], counts[state.StatusInbox]
	data["Waits"], data["More"], data["Waiting"] = waits, total-len(waits), total
	return nil
}

type facts struct {
	latest map[int64]state.Attempt
	done   map[int64]bool
	unread map[int64]bool
	asked  map[int64]int
	prs    map[int64]string
}

func (b *Board) facts(ctx context.Context) (facts, error) {
	var f facts
	var err error
	if f.latest, err = b.st.LatestAttempts(ctx); err != nil {
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
	Attempt *state.Attempt
	PR      string
}

func (f facts) check(t state.Task) check {
	c := check{Task: t, State: "idle", PR: f.prs[t.ID]}
	failed, done := false, t.Status == state.StatusDone
	if a, ok := f.latest[t.ID]; ok {
		c.Attempt = &a
		failed = t.Status == state.StatusActive && f.failing(a)
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
