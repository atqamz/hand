package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func mergeExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func mergeFleet(t *testing.T, s *Store, id, name string) {
	t.Helper()
	mergeExec(t, s, `INSERT INTO fleet(only, id, name) VALUES (1, ?, ?)`, id, name)
}

func TestMergeReadsOneSourceSnapshot(t *testing.T) {
	srcDir, dstDir := t.TempDir(), t.TempDir()
	srcPath, dstPath := filepath.Join(srcDir, "hand.db"), filepath.Join(dstDir, "hand.db")
	src, err := Open(srcPath, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := Open(dstPath, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	mergeFleet(t, src, "f000000000001", "source")
	mergeFleet(t, dst, "f000000000002", "destination")
	mergeExec(t, src, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
	mergeExec(t, src, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
	mergeExec(t, dst, `INSERT INTO project(name, repo, settings, created_at) VALUES ('d', 'd', '', '2026-11-01T00:00:00Z')`)
	mergeExec(t, dst, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'd', 'dest', 'dest', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)

	mergeAfterSourceCounts = func() {
		db, err := sql.Open("sqlite", "file:"+srcPath+"?_pragma=busy_timeout(10000)")
		if err != nil {
			t.Error(err)
			return
		}
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (2, 'p', 'late', 'late', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { mergeAfterSourceCounts = nil })

	rep, err := Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
	if err != nil {
		t.Fatal(err)
	}
	var tasks int64
	if err := dst.db.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 2 {
		t.Fatalf("merged tasks = %d, want 2: the source was read after the snapshot", tasks)
	}
	for _, c := range rep.Counts {
		if c.Table == "task" && c.Merged != 2 {
			t.Fatalf("reported tasks = %d, want 2", c.Merged)
		}
	}
	var sourceTasks int64
	if err := src.db.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&sourceTasks); err != nil {
		t.Fatal(err)
	}
	if sourceTasks != 2 {
		t.Fatalf("source tasks = %d, want the concurrent write to land", sourceTasks)
	}
}

func TestTextRewriterLeavesPathsAnchorsUUIDsAndCodeAlone(t *testing.T) {
	refs := &mergeRefs{
		ids:   map[string]map[int64]bool{"t": {1: true}, "a": {1: true, 2: true}, "r": {1: true}, "d": {1: true, 10: true}, "w": {1: true}, "i": {1: true}},
		off:   map[string]int64{"t": 500, "a": 500, "r": 500, "d": 500, "w": 500, "i": 500},
		srcID: "f000000000001", dstID: "f000000000002",
	}
	w := newTextRewriter(refs, "", "")
	w.cell("report", "1", "body")
	for _, kept := range []string{
		"r1.png",
		"ledger-light-r1.png",
		"ident-a2-home.png",
		"brainstorm.md#7-data-model-d1",
		"wp-scratch-t183-cont",
		"/(r1|640)/",
		"routines/r1",
		"45374764-a190-4f00-8000-000000000000",
		"wrangler d1 create",
		"`wrangler d1 migrations apply`",
		`"r6"`,
		`\"r6\"`,
	} {
		if got := w.text(kept); got != kept {
			t.Fatalf("%q became %q", kept, got)
		}
	}
	for in, want := range map[string]string{
		"see t1 and a2.":            "see t501 and a502.",
		"see r1,":                   "see r501,",
		"d10":                       "d510",
		"the `foo bug in t1 and a1": "the `foo bug in t501 and a501",
	} {
		if got := w.text(in); got != want {
			t.Fatalf("%q became %q, want %q", in, got, want)
		}
	}
	if got := w.text("see d1"); got != "see d1" {
		t.Fatalf("d1 became %q", got)
	}
	if len(w.cells) == 0 {
		t.Fatal("the short decision ref was not reported ambiguous")
	}
}

func TestTextDigestCoversEveryTextColumn(t *testing.T) {
	s, _ := openTest(t)
	mergeExec(t, s, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
	mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
	mergeExec(t, s, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '[]', '/w1', 'b', 'stopped', '2026-11-01T00:00:00Z')`)
	mergeExec(t, s, `INSERT INTO event(at, kind, task_id, detail) VALUES ('2026-11-01T00:00:00Z', 'task.added', 1, '')`)
	first, err := textDigest(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	mergeExec(t, s, `UPDATE attempt SET worktree = '/w2'`)
	second, err := textDigest(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("textDigest ignores attempt.worktree")
	}
	mergeExec(t, s, `UPDATE event SET kind = 'task.done'`)
	third, err := textDigest(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("textDigest ignores event.kind")
	}
}

func TestMergeRefusesLiveAttemptsInTheTransaction(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		t.Run(side, func(t *testing.T) {
			srcDir, dstDir := t.TempDir(), t.TempDir()
			srcPath, dstPath := filepath.Join(srcDir, "hand.db"), filepath.Join(dstDir, "hand.db")
			src, err := Open(srcPath, clock)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			dst, err := Open(dstPath, clock)
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			mergeFleet(t, src, "f000000000001", "source")
			mergeFleet(t, dst, "f000000000002", "destination")
			for _, s := range []*Store{src, dst} {
				mergeExec(t, s, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
				mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
				mergeExec(t, s, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '[]', '/w', 'b', 'stopped', '2026-11-01T00:00:00Z')`)
			}
			live := src
			if side == "destination" {
				live = dst
			}
			mergeExec(t, live, `UPDATE attempt SET status = 'running' WHERE id = 1`)
			_, err = Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "live attempt") {
				t.Fatalf("merge = %v, want a live-attempt conflict", err)
			}
			var tasks int64
			if err := dst.db.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&tasks); err != nil {
				t.Fatal(err)
			}
			if tasks != 1 {
				t.Fatalf("destination tasks = %d, want 1: the transaction committed", tasks)
			}
		})
	}
}

func TestInspectFleetReportsReadErrors(t *testing.T) {
	for _, table := range []string{"attempt", "timer"} {
		t.Run(table, func(t *testing.T) {
			s, path := openTest(t)
			mergeFleet(t, s, "f000000000001", "fleet")
			mergeExec(t, s, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
			mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES
				(1, 'p', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z'),
				(2, 'p', 'two', 'two', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
			if table == "attempt" {
				mergeExec(t, s, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES
					(1, 1, 'claude', 'sonnet', 'low', '[]', '/w1', 'b1', 'running', '2026-11-01T00:00:00Z'),
					(2, 2, 'claude', 'sonnet', 'low', '[]', '/w2', 'b2', 'running', '2026-11-01T00:00:00Z')`)
			} else {
				mergeExec(t, s, `INSERT INTO timer(id, due_at, every, task_id, note, status, created_at) VALUES
					(1, '2027-01-01T00:00:00Z', 0, 1, 'n1', 'armed', '2026-11-01T00:00:00Z'),
					(2, '2027-01-01T00:00:00Z', 0, 2, 'n2', 'armed', '2026-11-01T00:00:00Z')`)
			}
			mergeExec(t, s, `ALTER TABLE `+table+` RENAME TO `+table+`_real`)
			mergeExec(t, s, `CREATE VIEW `+table+` AS SELECT * FROM `+table+`_real WHERE json(CASE WHEN id = 2 THEN '{bad' ELSE '{}' END) IS NOT NULL`)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectFleet(path); err == nil {
				t.Fatalf("InspectFleet hid a %s read error", table)
			}
		})
	}
}

func TestTextDigestReportsReadErrors(t *testing.T) {
	s, _ := openTest(t)
	mergeExec(t, s, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
	mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES
		(1, 'p', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z'),
		(2, 'p', 'two', 'two', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
	mergeExec(t, s, `ALTER TABLE task RENAME TO task_real`)
	mergeExec(t, s, `CREATE VIEW task AS SELECT * FROM task_real WHERE json(CASE WHEN id = 2 THEN '{bad' ELSE '{}' END) IS NOT NULL`)
	if _, err := textDigest(context.Background(), s.db); err == nil {
		t.Fatal("textDigest hid a read error")
	}
}

func TestMergeURLRewritesRefsWithoutTouchingFleetIDs(t *testing.T) {
	refs := &mergeRefs{
		ids:   map[string]map[int64]bool{"a": {1: true}, "d": {1: true}},
		off:   map[string]int64{"a": 1, "d": 1},
		srcID: "f000000000001", dstID: "fa1d1c0000002",
	}
	w := newTextRewriter(refs, "", "")
	got := w.url("http://127.0.0.1:8787/f000000000001/decision/d1")
	want := "http://127.0.0.1:8787/fa1d1c0000002/decision/d2"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestRowsAnyReportsReadErrors(t *testing.T) {
	s, _ := openTest(t)
	mergeExec(t, s, `CREATE TABLE probe(x INTEGER)`)
	mergeExec(t, s, `INSERT INTO probe VALUES (1), (2)`)
	rows, err := s.db.Query(`SELECT CASE WHEN x = 1 THEN 1 ELSE json('{bad') END FROM probe`)
	if err != nil {
		t.Fatal(err)
	}
	if broken, err := rowsAny(rows); err == nil || broken {
		t.Fatalf("rowsAny = %v, %v; want the read error", broken, err)
	}
}
