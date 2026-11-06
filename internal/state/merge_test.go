package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
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
		ids:   map[string]map[int64]bool{"t": {1: true}, "a": {1: true, 2: true, 123456: true}, "r": {1: true}, "d": {1: true, 10: true}, "w": {1: true}, "i": {1: true}},
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
		"`wrangler d1 migrations apply`",
		`"r6"`,
		`\"r6\"`,
		"see t1's goal",
		"see t1’s goal",
		"reports\\t1\\report.md",
		"a123456",
		"section d1.2",
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
		"see d1":                    "see d501",
	} {
		if got := w.text(in); got != want {
			t.Fatalf("%q became %q, want %q", in, got, want)
		}
	}
	var ambiguous []string
	for _, c := range w.cells {
		ambiguous = append(ambiguous, c.Refs)
	}
	if !strings.Contains(strings.Join(ambiguous, " "), "d1") || !strings.Contains(strings.Join(ambiguous, " "), "a123456") {
		t.Fatalf("short decisions and seven-character attempts were not reported ambiguous: %v", ambiguous)
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

func TestTextRewriterRewritesOnlySourceBoardURLs(t *testing.T) {
	refs := &mergeRefs{
		ids:   map[string]map[int64]bool{"t": {1: true}, "a": {1: true}, "r": {1: true}, "d": {10: true}},
		off:   map[string]int64{"t": 500, "a": 500, "r": 500, "d": 500},
		srcID: "f000000000001", dstID: "fa1d1c0000002",
	}
	w := newTextRewriter(refs, "", "")
	w.cell("task", "1", "goal")
	for in, want := range map[string]string{
		"http://127.0.0.1:8787/f000000000001/decision/d10":                 "http://127.0.0.1:8787/fa1d1c0000002/decision/d510",
		"http://h/f000000000001/task/t1#a1b2c3d":                           "http://h/fa1d1c0000002/task/t501#a1b2c3d",
		"http://h/f000000000001/task/t1?commit=a123456":                    "http://h/fa1d1c0000002/task/t501?commit=a123456",
		"http://h/f000000000001/task/t1?branch=hand/f000000000001/t1-a1":   "http://h/fa1d1c0000002/task/t501?branch=hand/f000000000001/t1-a1",
		"http://h/f000000000001/report/r1":                                 "http://h/fa1d1c0000002/report/r501",
		"http://h/f000000000001/task/t1/extra":                             "http://h/fa1d1c0000002/task/t501/extra",
		"HTTP://h/f000000000001/task/t1":                                   "HTTP://h/fa1d1c0000002/task/t501",
		"http://h/f000000000001/task/t1?from=hand%2Ff000000000001%2Ft1-a1": "http://h/fa1d1c0000002/task/t501?from=hand%2Ff000000000001%2Ft1-a1",
		"http://h/f000000000001/task/t1#d1":                                "http://h/fa1d1c0000002/task/t501#d1",
	} {
		if got := w.url(in); got != want {
			t.Fatalf("%q became %q, want %q", in, got, want)
		}
		if got := w.destination(in); got != want {
			t.Fatalf("destination %q became %q, want %q", in, got, want)
		}
	}
	for _, kept := range []string{
		"http://h/f000000000001/doc/reports/t1/report.md",
		"https://example.test/hand/f000000000001/t1-a1",
		"http://h/f000000000001/task/d1",
		"http://h/f000000000001/task/t999",
		"http://h/f000000000001/attempt/a1",
		"http://h/f000000000001/task/t1x",
	} {
		if got := w.url(kept); got != kept {
			t.Fatalf("%q became %q", kept, got)
		}
		if got := w.destination(kept); got != kept {
			t.Fatalf("destination %q became %q", kept, got)
		}
	}
	if w.changed["url"] != 18 || w.protected["url"] != 12 {
		t.Fatalf("url changed/protected = %d/%d, want 18/12", w.changed["url"], w.protected["url"])
	}
}

func TestMergeRewritesOnlySourceBoardURLs(t *testing.T) {
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
	mergeExec(t, dst, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', 'p', '', '2026-11-01T00:00:00Z')`)
	goal := "http://h/f000000000001/task/t1#a1b2c3d http://h/f000000000001/doc/reports/t1/report.md " +
		"http://h/f000000000001/task/t1?commit=a123456 http://h/f000000000001/task/t1?branch=hand/f000000000001/t1-a1 " +
		"https://example.test/hand/f000000000001/t1-a1"
	mergeExec(t, src, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', 'one', ?, 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`, goal)
	mergeExec(t, dst, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', 'dest', ?, 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`, "http://h/f000000000001/task/t1")

	rep, err := Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
	if err != nil {
		t.Fatal(err)
	}
	want := "http://h/f000000000002/task/t2#a1b2c3d http://h/f000000000001/doc/reports/t1/report.md " +
		"http://h/f000000000002/task/t2?commit=a123456 http://h/f000000000002/task/t2?branch=hand/f000000000001/t1-a1 " +
		"https://example.test/hand/f000000000001/t1-a1"
	var got string
	if err := dst.db.QueryRow(`SELECT goal FROM task WHERE id = 2`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("imported goal:\n got %q\nwant %q", got, want)
	}
	if err := dst.db.QueryRow(`SELECT goal FROM task WHERE id = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "http://h/f000000000002/task/t2" {
		t.Fatalf("destination goal = %q", got)
	}
	for _, r := range rep.Refs {
		if r.Kind == "url" && (r.Changed != 4 || r.Protected != 2) {
			t.Fatalf("url refs = %+v, want 4 changed and 2 protected", r)
		}
	}
}

func TestMergeRewritesAWholeOneDigitDecision(t *testing.T) {
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
		mergeExec(t, s, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, 'question', 'open', '', '', '2026-11-01T00:00:00Z', '')`)
	}
	mergeExec(t, src, `UPDATE task SET title = 'see d1', goal = 'd1.2 and d1.' WHERE id = 1`)
	mergeExec(t, src, `UPDATE decision SET question = 'choose d1' WHERE id = 1`)
	mergeExec(t, dst, `UPDATE task SET title = 'see d1', goal = 'd1.2 and d1.' WHERE id = 1`)

	rep, err := Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
	if err != nil {
		t.Fatal(err)
	}
	var title, goal string
	if err := dst.db.QueryRow(`SELECT title, goal FROM task WHERE id = 2`).Scan(&title, &goal); err != nil {
		t.Fatal(err)
	}
	if title != "see d2" || goal != "d1.2 and d2." {
		t.Fatalf("imported decision refs = %q / %q, want a whole d1 rewritten and d1.2 kept", title, goal)
	}
	var question string
	if err := dst.db.QueryRow(`SELECT question FROM decision WHERE id = 2`).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if question != "choose d2" {
		t.Fatalf("imported question = %q", question)
	}
	if err := dst.db.QueryRow(`SELECT title, goal FROM task WHERE id = 1`).Scan(&title, &goal); err != nil {
		t.Fatal(err)
	}
	if title != "see d1" || goal != "d1.2 and d1." {
		t.Fatalf("destination decision refs = %q / %q, want them untouched", title, goal)
	}
	found := false
	for _, c := range rep.Cells {
		if c.Table == "task" && c.Key == "2" && c.Column == "goal" {
			found = true
			if c.Refs != "d1" {
				t.Fatalf("ambiguous refs = %q, want d1 from d1.2", c.Refs)
			}
		}
	}
	if !found {
		t.Fatal("the partial d1 was not reported ambiguous")
	}
}

func TestMergeRekeysEventAmbiguityToTheRenumberedSeq(t *testing.T) {
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
	}
	mergeExec(t, src, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES
		(10, '2026-11-04T00:00:00Z', 'task.added', 1, 'commit a1b2c3d'),
		(11, '2026-11-04T02:00:00Z', 'task.added', 1, 'later')`)
	mergeExec(t, dst, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES
		(1, '2026-11-03T00:00:00Z', 'board.opened', NULL, ''),
		(2, '2026-11-04T01:00:00Z', 'task.added', 1, ''),
		(3, '2026-11-06T00:00:00Z', 'task.done', 1, '')`)

	rep, err := Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := dst.db.QueryRow(`SELECT seq FROM event WHERE detail = 'commit a1b2c3d'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("event seq = %d, want 2", seq)
	}
	found := false
	for _, c := range rep.Cells {
		if c.Table == "event" {
			found = true
			if c.Key != strconv.FormatInt(seq, 10) {
				t.Fatalf("event ambiguous key = %q, want the renumbered %d", c.Key, seq)
			}
		}
	}
	if !found {
		t.Fatal("the event ambiguity was not reported")
	}
}

func TestMergeRewritesURLsInEveryDestinationColumn(t *testing.T) {
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
	url := "http://h/f000000000001/decision/d1"
	want := "http://h/f000000000002/decision/d2"
	mergeExec(t, src, `INSERT INTO project(name, repo, settings, created_at) VALUES ('s', 's', '', '2026-11-01T00:00:00Z')`)
	mergeExec(t, src, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 's', 'one', 'one', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
	mergeExec(t, src, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, 'q', 'open', '', '', '2026-11-01T00:00:00Z', '')`)
	mergeExec(t, dst, `INSERT INTO project(name, repo, settings, created_at) VALUES ('p', ?, '', '2026-11-01T00:00:00Z')`, url)
	mergeExec(t, dst, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'p', ?, ?, 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`, url, url)
	mergeExec(t, dst, `INSERT INTO plan(task_id, revision, body, created_at) VALUES (1, 1, ?, '2026-11-01T00:00:00Z')`, url)
	mergeExec(t, dst, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, ?, 'answered', ?, '', '2026-11-01T00:00:00Z', '')`, url, url)
	mergeExec(t, dst, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', ?, '/w', 'b', 'stopped', '2026-11-01T00:00:00Z')`, url)
	mergeExec(t, dst, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', ?, '2026-11-01T00:00:00Z')`, url)
	mergeExec(t, dst, `INSERT INTO timer(id, due_at, every, task_id, note, status, created_at) VALUES (1, '2027-01-01T00:00:00Z', 0, 1, ?, 'fired', '2026-11-01T00:00:00Z')`, url)
	mergeExec(t, dst, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES (1, '2026-11-01T00:00:00Z', 'board.opened', NULL, ?)`, url)
	mergeExec(t, dst, `INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, ?, '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`, url)

	rep, err := Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT repo FROM project WHERE name = 'p'`,
		`SELECT title FROM task WHERE id = 1`,
		`SELECT goal FROM task WHERE id = 1`,
		`SELECT body FROM plan WHERE task_id = 1`,
		`SELECT question FROM decision WHERE id = 1`,
		`SELECT answer FROM decision WHERE id = 1`,
		`SELECT argv FROM attempt WHERE id = 1`,
		`SELECT body FROM report WHERE id = 1`,
		`SELECT note FROM timer WHERE id = 1`,
		`SELECT detail FROM event WHERE seq = 1`,
		`SELECT body FROM supervisor_input WHERE id = 1`,
	} {
		var got string
		if err := dst.db.QueryRow(q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", q, got, want)
		}
	}
	for _, r := range rep.Refs {
		if r.Kind == "url" && r.Changed != 11 {
			t.Fatalf("url refs = %+v, want 11 destination URLs rewritten", r)
		}
	}
}

func TestMergeRefusesTextThatWouldPassItsLimit(t *testing.T) {
	for _, tc := range []struct {
		name, column, want string
		write              func(t *testing.T, s *Store)
	}{
		{
			name:   "task title",
			column: "title",
			want:   "task t10 title",
			write: func(t *testing.T, s *Store) {
				mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (9, 'p', 'nine', 'nine', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
				mergeExec(t, s, `UPDATE task SET title = ? WHERE id = 9`, strings.Repeat("x", 197)+" t9")
			},
		},
		{
			name:   "timer note",
			column: "note",
			want:   "timer w1 note",
			write: func(t *testing.T, s *Store) {
				mergeExec(t, s, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (9, 'p', 'nine', 'nine', 'done', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z')`)
				mergeExec(t, s, `INSERT INTO timer(id, due_at, every, task_id, note, status, created_at, ended_at, waits) VALUES (1, '2027-01-01T00:00:00Z', 0, 1, ?, 'fired', '2026-11-01T00:00:00Z', '', 0)`, strings.Repeat("x", 197)+" t9")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			}
			tc.write(t, src)
			_, err = Merge(context.Background(), srcPath, dstPath, MergeOptions{SourceHome: srcDir, DestHome: dstDir})
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "200") {
				t.Fatalf("merge = %v, want a %s limit conflict", err, tc.want)
			}
			var n int64
			if err := dst.db.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("destination tasks = %d, want the refusal to roll back", n)
			}
		})
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
