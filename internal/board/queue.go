package board

import (
	"context"
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
	failures, err := b.failures(ctx)
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

func (b *Board) failures(ctx context.Context) ([]waiting, error) {
	tasks, err := b.st.Tasks(ctx, []string{state.StatusActive}, maxCards)
	if err != nil {
		return nil, err
	}
	var out []waiting
	for _, t := range tasks {
		attempts, err := b.st.Attempts(ctx, t.ID, 1)
		if err != nil {
			return nil, err
		}
		if len(attempts) == 0 {
			continue
		}
		a := attempts[0]
		failed, err := b.failing(ctx, a)
		if err != nil {
			return nil, err
		}
		if failed {
			out = append(out, waiting{Kind: "failure", Ref: state.AttemptRef(a.ID), Title: t.Title, Task: t, Attempt: &a})
		}
	}
	slices.SortFunc(out, func(x, y waiting) int { return int(x.Attempt.ID - y.Attempt.ID) })
	return out, nil
}

func (b *Board) failing(ctx context.Context, a state.Attempt) (bool, error) {
	switch a.Status {
	case state.AttemptFailed, state.AttemptInterrupted:
		return true, nil
	case state.AttemptExited:
		reports, err := b.st.Reports(ctx, state.ReportFilter{AttemptID: a.ID}, maxCards)
		if err != nil {
			return false, err
		}
		return !slices.ContainsFunc(reports, func(r state.Report) bool { return r.Status == state.ReportDone }), nil
	}
	return false, nil
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
	return t.UTC().Format("Jan 2 15:04")
}

type check struct {
	Task    state.Task
	State   string
	Attempt *state.Attempt
	PR      string
}

func (b *Board) check(ctx context.Context, t state.Task) (check, error) {
	c := check{Task: t, State: "idle"}
	attempts, err := b.st.Attempts(ctx, t.ID, 1)
	if err != nil {
		return c, err
	}
	reports, err := b.st.Reports(ctx, state.ReportFilter{TaskID: t.ID}, 20)
	if err != nil {
		return c, err
	}
	for i := len(reports) - 1; i >= 0 && c.PR == ""; i-- {
		if links := PRLinks(reports[i].Body); len(links) > 0 {
			c.PR = links[len(links)-1]
		}
	}
	asked, err := b.st.OpenDecisionCount(ctx, t.ID)
	if err != nil {
		return c, err
	}
	unread, err := b.st.Reports(ctx, state.ReportFilter{TaskID: t.ID, Unacked: true}, 1)
	if err != nil {
		return c, err
	}
	failed, done := false, t.Status == state.StatusDone
	if len(attempts) > 0 {
		c.Attempt = &attempts[0]
		if t.Status == state.StatusActive {
			if failed, err = b.failing(ctx, *c.Attempt); err != nil {
				return c, err
			}
		}
		done = done || slices.ContainsFunc(reports, func(r state.Report) bool { return r.AttemptID == c.Attempt.ID && r.Status == state.ReportDone })
	}
	switch {
	case failed:
		c.State = "failing"
	case asked > 0 || len(unread) > 0:
		c.State = "waiting"
	case c.Attempt != nil && c.Attempt.Live():
		c.State = "running"
	case done:
		c.State = "passing"
	}
	return c, nil
}
