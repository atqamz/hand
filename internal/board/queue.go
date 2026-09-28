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
	excerptLines  = 12
	screenLines   = 20
	blockedAsking = "The supervisor's screen needs a key"
)

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
	add := func(w waiting) {
		total++
		if len(waits) < maxWaits {
			waits = append(waits, w)
		}
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
		excerpt := strings.Split(strings.TrimSpace(r.Body), "\n")
		add(waiting{Kind: "report", Ref: state.ReportRef(r.ID), Title: r.Summary(), Task: t, Report: &r, Excerpt: strings.Join(excerpt[:min(len(excerpt), excerptLines)], "\n"), Cut: len(excerpt) > excerptLines})
	}
	total += unacked - len(unread)
	if live && sup.Session != "" && (sup.Status == state.AttemptInterrupted || sup.Status == state.AttemptExited) {
		add(waiting{Kind: "resume", Ref: state.SupervisorRef(sup.ID), Title: "The supervisor stopped unexpectedly", Sup: &sup})
		data["Resumable"] = false
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
