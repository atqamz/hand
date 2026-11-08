package board

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type needsView struct {
	supervisorView
	Controls   bool
	Base       string
	Token      string
	Local      func(string) string
	Titles     func(string) string
	Multi      bool
	Notice     string
	Waits      []waiting
	Waiting    int
	More       int
	Worst      string
	WorstText  string
	WorstWord  string
	WorstCount int
	Cleared    string
	Summary    string
}

type waitView struct {
	R    needsView
	W    waiting
	Open bool
}

func view(root needsView, w waiting, open bool) waitView {
	return waitView{R: root, W: w, Open: open}
}

type decideRoot struct {
	Base   string
	Token  string
	Notice string
}

type decidePick struct {
	R     decideRoot
	D     state.Decision
	Needs bool
}

func pick(root any, d state.Decision, needs bool) decidePick {
	var r decideRoot
	switch root := root.(type) {
	case needsView:
		r = decideRoot{Base: root.Base, Token: root.Token, Notice: root.Notice}
	case map[string]any:
		r.Base, _ = root["Base"].(string)
		r.Token, _ = root["Token"].(string)
		r.Notice, _ = root["Notice"].(string)
	}
	return decidePick{R: r, D: d, Needs: needs}
}

func (b *Board) buildNeeds(ctx context.Context, f facts, sv supervisorView, c viewContext) (needsView, error) {
	v := needsView{
		supervisorView: sv,
		Controls:       c.Controls,
		Base:           c.Base,
		Token:          c.Token,
		Local:          c.Local,
		Titles:         c.Titles,
		Multi:          c.Multi,
	}
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return v, err
	}
	var waits []waiting
	total := 0
	add := func(w waiting) {
		total++
		w.Title = clip(w.Title, titleRunes)
		waits = append(waits, w)
	}
	if sv.Blocked && sv.Sup != nil {
		title := sv.Hint
		if title == "" {
			title = blockedAsking
		}
		pressed, err := b.st.LastKeys(ctx, state.SupervisorRef(sv.Sup.ID))
		if err != nil {
			return v, err
		}
		w := waiting{Kind: "blocked", Ref: state.SupervisorRef(sv.Sup.ID), Title: title, Hint: sv.Hint, Screen: lastLines(sv.Screen, screenLines), Revision: sv.Revision, Digest: sv.Digest, Pressed: pressed, Sup: sv.Sup, Prompt: blockedPrompt(sv.Screen)}
		if w.Prompt.Headline != "" {
			w.Title = w.Prompt.Headline
		}
		add(w)
	}
	if sv.Limited != "" && sv.Sup != nil {
		add(waiting{Kind: "limited", Ref: state.SupervisorRef(sv.Sup.ID), Title: sv.Limited, Sup: sv.Sup})
	}
	supervised := sv.Sup != nil && sv.Sup.Status == state.AttemptRunning
	liveAttempts, err := b.st.LiveAttempts(ctx)
	if err != nil {
		return v, err
	}
	signals, err := b.signals(ctx, liveAttempts)
	if err != nil {
		return v, err
	}
	workers, err := b.workers(ctx, liveAttempts, signals, supervised, true)
	if err != nil {
		return v, err
	}
	for _, w := range workers {
		add(w)
	}
	decisions, err := b.st.OpenDecisions(ctx, 0, maxWaits)
	if err != nil {
		return v, err
	}
	asked, err := b.st.OpenDecisionCount(ctx, 0)
	if err != nil {
		return v, err
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
			return v, err
		}
		add(waiting{Kind: "decision", Ref: state.DecisionRef(d.ID), Title: d.Headline(), Task: t, Decision: &d})
	}
	total += asked - len(decisions)
	failures, err := b.failures(ctx, f)
	if err != nil {
		return v, err
	}
	for _, w := range failures {
		add(w)
	}
	kept, err := b.st.KeptAttempts(ctx)
	if err != nil {
		return v, err
	}
	for _, a := range kept {
		t, err := task(a.TaskID)
		if err != nil {
			return v, err
		}
		add(waiting{Kind: "kept", Ref: state.AttemptRef(a.ID), Title: "Worktree kept: " + a.Kept, Task: t, Attempt: &a})
	}
	if text, cmd := b.artifactWarning(); text != "" {
		add(waiting{Kind: "artifacts", Title: text, Command: cmd})
	}
	unread, err := b.st.Reports(ctx, state.ReportFilter{Unacked: true, Oldest: true}, maxWaits)
	if err != nil {
		return v, err
	}
	unacked, err := b.st.UnackedReportCount(ctx)
	if err != nil {
		return v, err
	}
	for _, r := range unread {
		t, err := task(r.TaskID)
		if err != nil {
			return v, err
		}
		_, rest, _ := strings.Cut(strings.TrimSpace(r.Body), "\n")
		excerpt, cut := excerptOf(rest)
		add(waiting{Kind: "report", Ref: state.ReportRef(r.ID), Title: markers.Replace(r.Summary()), Task: t, Report: &r, Excerpt: excerpt, Cut: cut})
	}
	total += unacked - len(unread)
	resume := sv.Sup != nil && sv.Sup.Session != "" && (sv.Sup.Status == state.AttemptInterrupted || sv.Sup.Status == state.AttemptExited)
	if resume {
		add(waiting{Kind: "resume", Ref: state.SupervisorRef(sv.Sup.ID), Title: "The supervisor stopped unexpectedly", Sup: sv.Sup})
	}
	if !supervised && !resume {
		waitsOnOne := len(liveAttempts) > 0 || sv.Pending > 0
		if !waitsOnOne {
			cursor := int64(0)
			if sv.Sup != nil {
				cursor = sv.Sup.WakeCursor
			}
			answered, err := b.st.EventsAfter(ctx, cursor, []string{"decision.answered"}, 1)
			if err != nil {
				return v, err
			}
			waitsOnOne = len(answered) > 0
		}
		if waitsOnOne {
			add(waiting{Kind: "nosup", Title: "Work is waiting for a supervisor"})
		}
	}
	if supervised && !sv.Sup.Switching() && sv.Pending == 0 && (sv.Agent == "idle" || sv.Agent == "done") && sv.Limited == "" && sv.Stale == "" {
		u, err := b.st.Unwatched(ctx, true)
		if err != nil {
			return v, err
		}
		if u.Stalls > 0 {
			tasks := strconv.Itoa(len(u.Tasks)) + " active task"
			if len(u.Tasks) != 1 {
				tasks += "s"
			}
			add(waiting{Kind: "stalled", Ref: state.SupervisorRef(sv.Sup.ID), Title: "Supervisor idle for " + u.IdleFor() + " with " + tasks + " and no running attempt", Sup: sv.Sup})
		}
	}
	if len(waits) == 0 {
		cleared, err := b.st.RecentEventsOf(ctx, []string{"decision.answered", "report.acked", "supervisor.keys", "attempt.keys"}, 1)
		if err != nil {
			return v, err
		}
		if len(cleared) > 0 {
			v.Cleared = cleared[0].At
		}
		var parts []string
		if n := len(liveAttempts); n > 0 {
			parts = append(parts, strconv.Itoa(n)+" running")
		}
		if n := counts[state.StatusInbox]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" inbox")
		}
		v.Summary = strings.Join(parts, " · ")
	}
	slices.SortStableFunc(waits, func(x, y waiting) int { return x.Rank() - y.Rank() })
	waits = waits[:min(len(waits), maxWaits)]
	v.Waits, v.More, v.Waiting = waits, total-len(waits), total
	v.WorstCount = total
	if len(waits) > 0 {
		v.Worst, v.WorstText, v.WorstWord = waits[0].Kind, strings.TrimSpace(waits[0].Ref+" "+waits[0].Title), "waiting"
		if waits[0].Tone() == "fail" {
			n := 0
			for _, w := range waits {
				if w.Word() == waits[0].Word() {
					n++
				}
			}
			v.WorstWord, v.WorstCount = strings.ToLower(waits[0].Word()), n
		}
	}
	return v, nil
}
