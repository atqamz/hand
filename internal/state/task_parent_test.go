package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAVersion19DatabaseOnDiskMigratesToTwentyWithRootTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{
		schema, migrations[0], migrations[1], migrations[2], `PRAGMA user_version = 19`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'one', '', 'inbox', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z'), (2, 'hand', 'two', '', 'done', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`,
	} {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := Open(path, clock)
		if err != nil {
			t.Fatal(err)
		}
		var v, idx int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 20 {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'task_parent'`).Scan(&idx); err != nil || idx != 1 {
			t.Fatalf("task_parent index = %d, %v", idx, err)
		}
		tasks, err := s.Tasks(context.Background(), nil, 10)
		if err != nil || len(tasks) != 2 || tasks[0].ParentID != 0 || tasks[1].ParentID != 0 {
			t.Fatalf("tasks = %+v, %v", tasks, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func seedTree(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openTest(t)
	seedProject(t, s)
	return s, context.Background()
}

func addTasks(t *testing.T, s *Store, n int) {
	t.Helper()
	for range n {
		if _, err := s.AddTask(context.Background(), "hand", "task", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func eventsOf(t *testing.T, s *Store, kind string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT task_id || ' ' || detail FROM event WHERE kind = ? ORDER BY seq`, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestAddTaskUnderRecordsTheParentAndRefusesAMissingOne(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 1)
	child, err := s.AddTaskUnder(ctx, 1, "hand", "child", "")
	if err != nil || child.ParentID != 1 {
		t.Fatalf("child = %+v, %v", child, err)
	}
	got, err := s.Task(ctx, child.ID)
	if err != nil || got.ParentID != 1 {
		t.Fatalf("Task = %+v, %v", got, err)
	}
	if _, err := s.AddTaskUnder(ctx, 99, "hand", "orphan", ""); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "t99") {
		t.Fatalf("missing parent err = %v", err)
	}
	if n, _ := s.CountTasks(ctx); n[StatusInbox] != 2 {
		t.Fatalf("a refused add left a task: %v", n)
	}
	if ev := eventsOf(t, s, "task.added"); !slices.Equal(ev, []string{"1 ", "2 t1"}) {
		t.Fatalf("task.added events = %q", ev)
	}
}

func TestATaskMayChildUnderADoneOrAbandonedParentInAnotherProject(t *testing.T) {
	s, ctx := seedTree(t)
	otherRepo, _ := filepath.Abs("/home/me/other")
	if _, err := s.AddProject(ctx, "other", otherRepo, ""); err != nil {
		t.Fatal(err)
	}
	addTasks(t, s, 2)
	for _, id := range []int64{1, 2} {
		if _, err := s.Transition(ctx, id, StatusAbandoned); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []int64{1, 2} {
		if c, err := s.AddTaskUnder(ctx, p, "other", "follow-up", ""); err != nil || c.ParentID != p {
			t.Fatalf("child under t%d = %+v, %v", p, c, err)
		}
	}
	if _, err := s.SetParent(ctx, 2, 1); err != nil {
		t.Fatalf("a finished task may be re-parented: %v", err)
	}
}

func TestSetParentSetsChangesClearsAndRepeats(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 3)
	steps := []struct {
		parent int64
		events int
	}{{2, 1}, {2, 1}, {3, 2}, {0, 3}, {0, 3}}
	for _, st := range steps {
		got, err := s.SetParent(ctx, 1, st.parent)
		if err != nil || got.ParentID != st.parent {
			t.Fatalf("SetParent(%d) = %+v, %v", st.parent, got, err)
		}
		if ev := eventsOf(t, s, "task.parented"); len(ev) != st.events {
			t.Fatalf("after parent %d: events %q, want %d", st.parent, ev, st.events)
		}
	}
	if ev := eventsOf(t, s, "task.parented"); !slices.Equal(ev, []string{"1 t2", "1 t3", "1 root"}) {
		t.Fatalf("task.parented events = %q", ev)
	}
}

func TestSetParentRefusesSelfCyclesAndMissingTasks(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 3)
	for _, p := range [][2]int64{{2, 1}, {3, 2}} {
		if _, err := s.SetParent(ctx, p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id, parent int64
		want       error
		text       string
	}{
		{1, 1, ErrConflict, "under itself"},
		{1, 2, ErrConflict, "t2 is already below it (t2 > t1)"},
		{1, 3, ErrConflict, "t3 is already below it (t3 > t2 > t1)"},
		{99, 1, ErrNotFound, "t99"},
		{1, 99, ErrNotFound, "t99"},
	} {
		_, err := s.SetParent(ctx, tc.id, tc.parent)
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
			t.Fatalf("SetParent(%d, %d) = %v, want %v %q", tc.id, tc.parent, err, tc.want, tc.text)
		}
	}
	if got, _ := s.Task(ctx, 1); got.ParentID != 0 {
		t.Fatalf("a refused change moved t1 under t%d", got.ParentID)
	}
	if ev := eventsOf(t, s, "task.parented"); len(ev) != 2 {
		t.Fatalf("a refused change appended an event: %q", ev)
	}
}

func TestTheCycleGuardEndsOnACorruptCycle(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 3)
	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF; UPDATE task SET parent_id = 2 WHERE id = 1; UPDATE task SET parent_id = 1 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetParent(ctx, 3, 1); err != nil {
		t.Fatalf("t3 under a corrupt loop it is not part of: %v", err)
	}
	if _, err := s.SetParent(ctx, 1, 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestChildrenAndChildCountsAndTreeOrder(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 1)
	for _, p := range []int64{1, 1, 2} {
		if _, err := s.AddTaskUnder(ctx, p, "hand", "child", ""); err != nil {
			t.Fatal(err)
		}
	}
	addTasks(t, s, 1)
	kids, err := s.Children(ctx, 1)
	if err != nil || len(kids) != 2 || kids[0].ID != 2 || kids[1].ID != 3 {
		t.Fatalf("Children = %+v, %v", kids, err)
	}
	counts, err := s.ChildCounts(ctx)
	if err != nil || len(counts) != 2 || counts[1] != 2 || counts[2] != 1 {
		t.Fatalf("ChildCounts = %v, %v", counts, err)
	}
	if _, err := s.Transition(ctx, 3, StatusAbandoned); err != nil {
		t.Fatal(err)
	}
	if n, err := s.OpenChildCount(ctx, 1); err != nil || n != 1 {
		t.Fatalf("OpenChildCount = %d, %v", n, err)
	}
	all, _, err := s.SearchTasks(ctx, TaskQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, tk := range Tree(all) {
		order = append(order, tk.ID)
	}
	if want := []int64{1, 2, 4, 3, 5}; !slices.Equal(order, want) {
		t.Fatalf("Tree order = %v, want %v", order, want)
	}
	for _, tk := range all {
		if tk.ID == 4 && tk.ParentID != 2 {
			t.Fatalf("SearchTasks lost the parent: %+v", tk)
		}
	}
	done, err := s.Transition(ctx, 1, StatusActive)
	if err != nil || done.ParentID != 0 {
		t.Fatalf("Transition = %+v, %v", done, err)
	}
}

func TestImportFleetOffsetsParentIDs(t *testing.T) {
	fx := newImportStateFixture(t)
	ctx := context.Background()
	src, err := Open(filepath.Join(fx.srcHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddTaskUnder(ctx, 1, "app", "child", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddTask(ctx, "app", "late root", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.SetParent(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	dst, err := Open(filepath.Join(fx.dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := dst.AddTask(ctx, "app", "filler", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	dry := fx.options()
	dry.DryRun = true
	before, err := ImportFleet(ctx, dry)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ImportFleet(ctx, dry)
	if err != nil || again.TextSHA256 != before.TextSHA256 {
		t.Fatalf("dry-run hash changed: %v", err)
	}
	if _, err := ImportFleet(ctx, fx.options()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]int{6: 8, 7: 6, 8: 0} {
		if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task WHERE id = `+strconv.Itoa(id)+` AND COALESCE(parent_id, 0) = `+strconv.Itoa(want)); n != 1 {
			t.Fatalf("task %d: parent %d not carried", id, want)
		}
	}
}
