package state

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func (h *historyDB) decision(t *testing.T, task int64, question string) string {
	t.Helper()
	d, err := h.t.Ask(context.Background(), task, question)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("d%d", d.ID)
}

func (h *historyDB) task(t *testing.T, title string) string {
	t.Helper()
	task, err := h.t.AddTask(context.Background(), "hand", title, "")
	if err != nil {
		t.Fatal(err)
	}
	return TaskRef(task.ID)
}

func TestSearchFindsDecisionsAndTaskTitles(t *testing.T) {
	h := newHistory(t)
	task := h.attempt.TaskID
	d := h.decision(t, task, "Wording: kiln or oven?\nOptions: 1. kiln 2. oven")
	tt := h.task(t, "copy: kiln everywhere")
	res := h.search(t, HistoryQuery{Q: "kiln", Limit: 50})
	if got := refs(res); !slices.Equal(got, []string{d, tt}) {
		t.Fatalf("hits = %v", got)
	}
	for _, hit := range res.Hits {
		switch hit.Ref {
		case d:
			if hit.Who != "decision" || hit.Task != TaskRef(task) || !strings.Contains(hit.Text, "«kiln»") {
				t.Fatalf("decision hit = %+v", hit)
			}
		case tt:
			if hit.Who != "task" || hit.Task != tt || !strings.Contains(hit.Text, "«kiln»") {
				t.Fatalf("task hit = %+v", hit)
			}
		}
	}
	e, err := h.t.HistoryEntry(context.Background(), d)
	if err != nil || e.Who != "decision" || e.Text != "Wording: kiln or oven?\nOptions: 1. kiln 2. oven" || e.Task != TaskRef(task) {
		t.Fatalf("%s = %+v, %v", d, e, err)
	}
	if _, err := h.t.Answer(context.Background(), mustDecisionID(t, d), "oven, then", "operator"); err != nil {
		t.Fatal(err)
	}
	if e, err = h.t.HistoryEntry(context.Background(), d); err != nil || !strings.HasSuffix(e.Text, "\noven, then") {
		t.Fatalf("answered %s = %+v, %v", d, e, err)
	}
	if e, err = h.t.HistoryEntry(context.Background(), tt); err != nil || e.Who != "task" || e.Text != "copy: kiln everywhere" || e.Task != tt {
		t.Fatalf("%s = %+v, %v", tt, e, err)
	}
}

func mustDecisionID(t *testing.T, ref string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(ref, "d%d", &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestADecisionAnswerIsSearchableAtOnceAndATaskDeleteLeavesTheIndex(t *testing.T) {
	h := newHistory(t)
	ctx := context.Background()
	d := h.decision(t, h.attempt.TaskID, "Which colour?")
	if res := h.search(t, HistoryQuery{Q: "amber"}); res.Total != 0 {
		t.Fatalf("before the answer: %+v", res.Hits)
	}
	if _, err := h.t.Answer(ctx, mustDecisionID(t, d), "amber, with a ferry", "operator"); err != nil {
		t.Fatal(err)
	}
	res := h.search(t, HistoryQuery{Q: "amber ferry"})
	if got := refs(res); !slices.Equal(got, []string{d}) || !strings.Contains(res.Hits[0].Text, "«amber»") {
		t.Fatalf("after the answer: %+v", res.Hits)
	}
	if res := h.search(t, HistoryQuery{Q: "colour"}); res.Total != 1 {
		t.Fatalf("the question is lost after the answer: %+v", res.Hits)
	}
	tt := h.task(t, "birch schedule")
	if got := refs(h.search(t, HistoryQuery{Q: "birch"})); !slices.Equal(got, []string{tt}) {
		t.Fatalf("task insert: %v", got)
	}
	h.exec(t, `UPDATE task SET title = 'cedar schedule' WHERE id = ?`, mustDecisionID(t, "d"+tt[1:]))
	if res := h.search(t, HistoryQuery{Q: "birch"}); res.Total != 0 {
		t.Fatalf("the old title still finds %+v", res.Hits)
	}
	if got := refs(h.search(t, HistoryQuery{Q: "cedar"})); !slices.Equal(got, []string{tt}) {
		t.Fatalf("task update: %v", got)
	}
	h.exec(t, `DELETE FROM task WHERE id = ?`, mustDecisionID(t, "d"+tt[1:]))
	if res := h.search(t, HistoryQuery{Q: "cedar"}); res.Total != 0 {
		t.Fatalf("a deleted task is still found: %+v", res.Hits)
	}
	h.exec(t, `DELETE FROM decision WHERE id = ?`, mustDecisionID(t, d))
	if res := h.search(t, HistoryQuery{Q: "amber OR colour"}); res.Total != 0 {
		t.Fatalf("a deleted decision is still found: %+v", res.Hits)
	}
	for _, f := range []string{"decision_fts", "task_fts"} {
		if _, err := h.t.db.Exec(fmt.Sprintf(`INSERT INTO %s(%s, rank) VALUES ('integrity-check', 1)`, f, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

func TestMigratingFrom26IndexesDecisionsAndTasksAlreadyThere(t *testing.T) {
	s, path := openTest(t)
	h := &historyDB{t: s}
	h.attempt = runningAttempt(t, s)
	for _, q := range []string{`DROP TRIGGER decision_fts_insert`, `DROP TRIGGER decision_fts_delete`, `DROP TRIGGER decision_fts_update`, `DROP TRIGGER task_fts_insert`, `DROP TRIGGER task_fts_delete`, `DROP TRIGGER task_fts_update`, `DROP TABLE decision_fts`, `DROP TABLE task_fts`, `DROP VIEW history_decision`, `DROP VIEW history_task`, `PRAGMA user_version = 26`} {
		h.exec(t, q)
	}
	d := h.decision(t, h.attempt.TaskID, "Pick a harbour name")
	if _, err := h.t.Answer(context.Background(), mustDecisionID(t, d), "quay", "operator"); err != nil {
		t.Fatal(err)
	}
	tt := h.task(t, "dredge the harbour")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h.t = s
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("schema = %d, %v", v, err)
	}
	if got := refs(h.search(t, HistoryQuery{Q: "harbour"})); !slices.Equal(got, []string{d, tt}) {
		t.Fatalf("the migrated index finds %v, want %v", got, []string{d, tt})
	}
	if got := refs(h.search(t, HistoryQuery{Q: "quay"})); !slices.Equal(got, []string{d}) {
		t.Fatalf("the migrated index misses an answer: %v", got)
	}
	for _, f := range []string{"decision_fts", "task_fts"} {
		if _, err := s.db.Exec(fmt.Sprintf(`INSERT INTO %s(%s, rank) VALUES ('integrity-check', 1)`, f, f)); err != nil {
			t.Fatalf("%s does not match its base rows: %v", f, err)
		}
	}
	d2 := h.decision(t, h.attempt.TaskID, "another harbour")
	if got := refs(h.search(t, HistoryQuery{Q: "another"})); !slices.Equal(got, []string{d2}) {
		t.Fatalf("a row added after the migration: %v", got)
	}
}

func TestDecisionAndTaskTextIsRedactedInSnippetsAndEntries(t *testing.T) {
	h := newHistory(t)
	secret := shape("gh", "p_", strings.Repeat("Ab3dE", 8))
	tail := secret[len("ghp_"):]
	d := h.decision(t, h.attempt.TaskID, "Rotate "+shape("token ", secret)+" before omega?")
	tt := h.task(t, "omega "+shape("token ", secret))
	res := h.search(t, HistoryQuery{Q: "omega", Limit: 50})
	if got := refs(res); !slices.Equal(got, []string{d, tt}) {
		t.Fatalf("hits = %v", got)
	}
	for _, hit := range res.Hits {
		if strings.Contains(hit.Text, tail) || !strings.Contains(hit.Text, "omega") {
			t.Fatalf("%s snippet = %q", hit.Ref, hit.Text)
		}
		e, err := h.t.HistoryEntry(context.Background(), hit.Ref)
		if err != nil || strings.Contains(e.Text, tail) || !strings.Contains(e.Text, "[redacted:github-token]") {
			t.Fatalf("%s entry = %q, %v", hit.Ref, e.Text, err)
		}
	}
}

func TestDecisionAndTaskHitsTakeTheWhoSinceAndTaskFilters(t *testing.T) {
	h := newHistory(t)
	other := h.task(t, "kiln for other")
	otherID := mustDecisionID(t, "d"+other[1:])
	d1 := h.decision(t, h.attempt.TaskID, "kiln one")
	d2 := h.decision(t, otherID, "kiln two")
	i := h.input(t, "2026-11-10T10:00:00Z", "kiln operator")
	h.exec(t, `UPDATE decision SET created_at = '2026-11-12T10:00:00Z'`)
	h.exec(t, `UPDATE task SET created_at = '2026-11-10T10:00:00Z' WHERE id = ?`, otherID)
	for _, c := range []struct {
		name string
		q    HistoryQuery
		want []string
	}{
		{"all", HistoryQuery{Q: "kiln"}, []string{d1, d2, i, other}},
		{"decision", HistoryQuery{Q: "kiln", Who: "decision"}, []string{d1, d2}},
		{"task", HistoryQuery{Q: "kiln", Who: "task"}, []string{other}},
		{"operator", HistoryQuery{Q: "kiln", Who: "operator"}, []string{i}},
		{"task filter", HistoryQuery{Q: "kiln", TaskID: otherID}, []string{d2, other}},
		{"since", HistoryQuery{Q: "kiln", Since: time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)}, []string{d1, d2}},
		{"context", HistoryQuery{Q: "kiln", Who: "decision", Context: 2}, []string{d1, d2}},
	} {
		res := h.search(t, c.q)
		got := refs(res)
		slices.Sort(c.want)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
		for _, hit := range res.Hits {
			if len(hit.Context) != 0 {
				t.Errorf("%s: %s has context", c.name, hit.Ref)
			}
		}
	}
}
