package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func editEvents(t *testing.T, s *Store) []string {
	t.Helper()
	var out []string
	for _, l := range eventLines(t, s) {
		if strings.HasPrefix(l, "task.edited ") {
			out = append(out, l)
		}
	}
	return out
}

func formerTitles(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var f string
	if err := s.db.QueryRow(`SELECT former_titles FROM task WHERE id = ?`, id).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestEditTaskChangesTitleGoalOrBothWithOneEventEach(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Old title", "old goal")
	got, changed, err := s.EditTask(ctx, task.ID, sp("  New title  "), nil)
	if err != nil || !changed || got.Title != "New title" || got.Goal != "old goal" {
		t.Fatalf("title edit = %+v, %v, %v", got, changed, err)
	}
	got, changed, err = s.EditTask(ctx, task.ID, nil, sp("new goal"))
	if err != nil || !changed || got.Title != "New title" || got.Goal != "new goal" {
		t.Fatalf("goal edit = %+v, %v, %v", got, changed, err)
	}
	got, changed, err = s.EditTask(ctx, task.ID, sp("Third"), sp("third goal"))
	if err != nil || !changed || got.Title != "Third" || got.Goal != "third goal" {
		t.Fatalf("both edit = %+v, %v, %v", got, changed, err)
	}
	got, changed, err = s.EditTask(ctx, task.ID, nil, sp(""))
	if err != nil || !changed || got.Goal != "" {
		t.Fatalf("clear goal = %+v, %v, %v", got, changed, err)
	}
	read, _ := s.Task(ctx, task.ID)
	if read.Title != "Third" || read.Goal != "" || read.Project != "hand" || read.Status != StatusInbox {
		t.Fatalf("stored = %+v", read)
	}
	want := []string{
		"task.edited title: Old title -> New title t1",
		"task.edited goal: old goal -> new goal t1",
		"task.edited title: New title -> Third t1",
		"task.edited goal: new goal -> third goal t1",
		"task.edited goal: third goal ->  t1",
	}
	if got := editEvents(t, s); !slices.Equal(got, want) {
		t.Fatalf("events = %q\nwant %q", got, want)
	}
}

func TestEditTaskWithTheSameValueWritesNothing(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Same", "goal")
	before := len(eventLines(t, s))
	for _, tc := range []struct{ title, goal *string }{{sp("Same"), nil}, {sp("  Same "), nil}, {nil, sp("goal")}, {sp("Same"), sp("goal")}} {
		got, changed, err := s.EditTask(ctx, task.ID, tc.title, tc.goal)
		if err != nil || changed || got.Title != "Same" {
			t.Fatalf("no-op = %+v, %v, %v", got, changed, err)
		}
	}
	if n := len(eventLines(t, s)); n != before {
		t.Fatalf("events %d -> %d", before, n)
	}
	if f := formerTitles(t, s, task.ID); f != "" {
		t.Fatalf("former_titles = %q", f)
	}
	if _, changed, err := s.EditTask(ctx, task.ID, sp("Same"), sp("other")); err != nil || !changed {
		t.Fatalf("one field of two changed: %v, %v", changed, err)
	}
	if f := formerTitles(t, s, task.ID); f != "" {
		t.Fatalf("a goal edit changed former_titles = %q", f)
	}
	if got := editEvents(t, s); len(got) != 1 {
		t.Fatalf("events = %q", got)
	}
}

func TestEditTaskRefusesAnInvalidTitleAndChangesNothing(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Keep", "goal")
	for _, title := range []string{"", "   ", strings.Repeat("x", 201), "a\nb", "a\rb"} {
		if _, _, err := s.EditTask(ctx, task.ID, sp(title), sp("changed")); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "task title must be one line of 1-200 characters") {
			t.Fatalf("title %q err = %v", title, err)
		}
	}
	if _, changed, err := s.EditTask(ctx, task.ID, sp(strings.Repeat("é", 200)), nil); err != nil || !changed {
		t.Fatalf("200 runes: %v, %v", changed, err)
	}
	read, _ := s.Task(ctx, task.ID)
	if read.Goal != "goal" || len([]rune(read.Title)) != 200 {
		t.Fatalf("stored = %+v", read)
	}
	if n := len(editEvents(t, s)); n != 1 {
		t.Fatalf("edit events = %d", n)
	}
}

func TestEditTaskAcceptsEveryStatusAndRefusesAnUnknownTask(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	var ids []int64
	for _, st := range statuses {
		task, _ := s.AddTask(ctx, "hand", "Title "+st, "")
		switch st {
		case StatusActive, StatusDone, StatusAbandoned:
			if _, err := s.Transition(ctx, task.ID, StatusActive); err != nil {
				t.Fatal(err)
			}
		}
		if st == StatusDone || st == StatusAbandoned {
			if _, err := s.Transition(ctx, task.ID, st); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, task.ID)
	}
	for i, id := range ids {
		got, changed, err := s.EditTask(ctx, id, sp("Edited "+statuses[i]), nil)
		if err != nil || !changed || got.Status != statuses[i] || got.Title != "Edited "+statuses[i] {
			t.Fatalf("%s: %+v, %v, %v", statuses[i], got, changed, err)
		}
	}
	if _, _, err := s.EditTask(ctx, 99, sp("x"), nil); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "task t99") {
		t.Fatalf("unknown task err = %v", err)
	}
}

func TestEditTaskClipsEachSideOfTheEventDetail(t *testing.T) {
	s, _ := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	long := strings.Repeat("é", 300)
	task, _ := s.AddTask(ctx, "hand", "t", long)
	if _, _, err := s.EditTask(ctx, task.ID, nil, sp(long+"x")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditTask(ctx, task.ID, nil, sp(strings.Repeat("ü", 301))); err != nil {
		t.Fatal(err)
	}
	got := editEvents(t, s)
	want := []string{
		"task.edited goal: " + long + " -> " + long + "... t1",
		"task.edited goal: " + long + "... -> " + strings.Repeat("ü", 300) + "... t1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q\nwant %q", got, want)
	}
	read, _ := s.Task(ctx, task.ID)
	if read.Goal != strings.Repeat("ü", 301) {
		t.Fatalf("stored goal was clipped: %d runes", len([]rune(read.Goal)))
	}
}

func TestEditTaskLeavesUpdatedAtAndEveryOtherColumnAlone(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "hand.db")
	s, err := Open(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Before", "")
	now = now.Add(48 * time.Hour)
	if _, _, err := s.EditTask(ctx, task.ID, sp("After"), sp("goal")); err != nil {
		t.Fatal(err)
	}
	read, _ := s.Task(ctx, task.ID)
	if read.UpdatedAt != task.UpdatedAt || read.CreatedAt != task.CreatedAt || read.Project != "hand" || read.Status != StatusInbox || read.ParentID != 0 {
		t.Fatalf("row = %+v, before %+v", read, task)
	}
	events, _ := s.EventsAfter(ctx, 0, nil, 100)
	last := events[len(events)-1]
	if last.Kind != "task.edited" || last.At != now.Format(time.RFC3339) {
		t.Fatalf("last event = %+v", last)
	}
}

func TestEditTaskKeepsALiveAttemptAndItsRowsUntouched(t *testing.T) {
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	before, err := s.Attempt(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditTask(context.Background(), a.TaskID, sp("Renamed"), nil); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Attempt(context.Background(), a.ID)
	if after.Status != before.Status || after.Worktree != before.Worktree || after.Branch != before.Branch {
		t.Fatalf("attempt changed: %+v -> %+v", before, after)
	}
}

func TestEditedTitleKeepsTheOldOnesInHistoryAndTaskSearch(t *testing.T) {
	h := newHistory(t)
	ctx := context.Background()
	ref := h.task(t, "alpha rollout")
	id := mustDecisionID(t, "d"+ref[1:])
	for _, title := range []string{"bravo rollout", "charlie rollout"} {
		if _, _, err := h.t.EditTask(ctx, id, sp(title), nil); err != nil {
			t.Fatal(err)
		}
	}
	if f := formerTitles(t, h.t, id); f != "bravo rollout\nalpha rollout" {
		t.Fatalf("former_titles = %q", f)
	}
	for _, w := range []string{"alpha", "bravo", "charlie"} {
		if got := refs(h.search(t, HistoryQuery{Q: w, Who: "task"})); !slices.Equal(got, []string{ref}) {
			t.Fatalf("history %s = %v", w, got)
		}
		_, total, err := h.t.SearchTasks(ctx, TaskQuery{Q: w, Limit: 10})
		if err != nil || total != 1 {
			t.Fatalf("SearchTasks %s = %d, %v", w, total, err)
		}
	}
	if _, total, _ := h.t.SearchTasks(ctx, TaskQuery{Q: "delta", Limit: 10}); total != 0 {
		t.Fatalf("SearchTasks delta = %d", total)
	}
	if _, _, err := h.t.EditTask(ctx, id, nil, sp("goal only")); err != nil {
		t.Fatal(err)
	}
	if got := refs(h.search(t, HistoryQuery{Q: "alpha", Who: "task"})); !slices.Equal(got, []string{ref}) {
		t.Fatalf("history after a goal edit = %v", got)
	}
	e, err := h.t.HistoryEntry(ctx, ref)
	if err != nil || !strings.HasPrefix(e.Text, "charlie rollout") {
		t.Fatalf("entry = %+v, %v", e, err)
	}
	if _, err := h.t.db.Exec(`INSERT INTO task_fts(task_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Fatalf("task_fts does not match its rows: %v", err)
	}
	h.exec(t, `DELETE FROM task WHERE id = ?`, id)
	if res := h.search(t, HistoryQuery{Q: "alpha"}); res.Total != 0 {
		t.Fatalf("a deleted task is still found: %+v", res.Hits)
	}
	if _, err := h.t.db.Exec(`INSERT INTO task_fts(task_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Fatalf("task_fts after delete: %v", err)
	}
}

func TestFormerTitlesKeepTheNewestTen(t *testing.T) {
	names := strings.Fields("amber basalt cobalt dune ember flint garnet harbor indigo jasper kelp lumen marble")
	h := newHistory(t)
	ctx := context.Background()
	ref := h.task(t, names[0])
	id := mustDecisionID(t, "d"+ref[1:])
	for _, n := range names[1:] {
		if _, _, err := h.t.EditTask(ctx, id, sp(n), nil); err != nil {
			t.Fatal(err)
		}
	}
	want := slices.Clone(names[2:12])
	slices.Reverse(want)
	if got := strings.Split(formerTitles(t, h.t, id), "\n"); !slices.Equal(got, want) {
		t.Fatalf("former = %q\nwant %q", got, want)
	}
	for i, n := range names {
		_, total, err := h.t.SearchTasks(ctx, TaskQuery{Q: n, Limit: 10})
		found := len(h.search(t, HistoryQuery{Q: n, Who: "task"}).Hits)
		if wantFound := 1 - min(1, max(0, 2-i)); err != nil || total != wantFound || found != wantFound {
			t.Fatalf("%s: SearchTasks = %d, history = %d, want %d (%v)", n, total, found, wantFound, err)
		}
	}
	if n := len(editEvents(t, h.t)); n != 12 {
		t.Fatalf("edit events = %d", n)
	}
}

func TestConcurrentEditsBothApplyInOrder(t *testing.T) {
	s, path := openTest(t)
	seedProject(t, s)
	ctx := context.Background()
	task, _ := s.AddTask(ctx, "hand", "Start", "")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, title := range []string{"First", "Second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := Open(path, clock)
			if err != nil {
				errs <- err
				return
			}
			defer other.Close()
			<-start
			_, _, err = other.EditTask(ctx, task.ID, sp(title), nil)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	read, _ := s.Task(ctx, task.ID)
	loser := map[string]string{"First": "Second", "Second": "First"}[read.Title]
	if loser == "" {
		t.Fatalf("title = %q", read.Title)
	}
	if f := formerTitles(t, s, task.ID); f != loser+"\nStart" {
		t.Fatalf("former_titles = %q, want %q", f, loser+"\nStart")
	}
	if n := len(editEvents(t, s)); n != 2 {
		t.Fatalf("edit events = %d", n)
	}
}

func TestAVersion28DatabaseMigratesToFormerTitles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{schema}
	stmts = append(stmts, migrations[:len(migrations)-1]...)
	stmts = append(stmts,
		`PRAGMA user_version = 28`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'granite quarry', 'g', 'inbox', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z'), (2, 'hand', 'marble', '', 'active', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`)
	for _, m := range stmts {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%.60s: %v", m, err)
		}
	}
	var old int
	if err := db.QueryRow(`SELECT count(*) FROM task_fts WHERE task_fts MATCH 'granite'`).Scan(&old); err != nil || old != 1 {
		t.Fatalf("v28 index finds %d, %v", old, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for round := range 2 {
		s, err := Open(path, clock)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		if f := formerTitles(t, s, 1); (round == 0) != (f == "") {
			t.Fatalf("round %d former_titles = %q", round, f)
		}
		h := &historyDB{t: s}
		if got := refs(h.search(t, HistoryQuery{Q: "granite", Who: "task"})); !slices.Equal(got, []string{"t1"}) {
			t.Fatalf("migrated index finds %v", got)
		}
		if round == 0 {
			if _, _, err := s.EditTask(ctx, 1, sp("basalt quarry"), nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.EditTask(ctx, 2, nil, sp("goal")); err != nil {
				t.Fatal(err)
			}
		}
		for _, w := range []string{"granite", "basalt"} {
			if got := refs(h.search(t, HistoryQuery{Q: w, Who: "task"})); !slices.Equal(got, []string{"t1"}) {
				t.Fatalf("round %d %s finds %v", round, w, got)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO task_fts(task_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
			t.Fatalf("task_fts does not match its rows: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportDropsTheFormerTitlesOfAnEditedTask(t *testing.T) {
	fx := newImportStateFixture(t)
	ctx := context.Background()
	src, err := Open(filepath.Join(fx.srcHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := src.EditTask(ctx, 1, sp("renamed zebra"), nil); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportFleet(ctx, fx.options()); err != nil {
		t.Fatal(err)
	}
	dst, err := Open(filepath.Join(fx.dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	task, err := dst.Task(ctx, 1)
	if err != nil || task.Title != "renamed zebra" {
		t.Fatalf("imported task = %+v, %v", task, err)
	}
	if f := formerTitles(t, dst, 1); f != "" {
		t.Fatalf("former_titles = %q", f)
	}
	if res, err := dst.SearchHistory(ctx, HistoryQuery{Q: "zebra", Who: "task", Limit: 5}); err != nil || res.Total != 1 {
		t.Fatalf("history = %+v, %v", res, err)
	}
}
