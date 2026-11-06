package state

import (
	"context"
	"database/sql"
	"path/filepath"
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
