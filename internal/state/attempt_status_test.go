package state

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestAVersion20DatabaseOnDiskMigratesToTwentyOneAndLiveAttemptsUseTheStatusIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{
		schema, migrations[0], migrations[1], migrations[2], migrations[3], `PRAGMA user_version = 20`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'one', '', 'active', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z'), (2, 'hand', 'two', '', 'active', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', '', '', '[]', '/w/1', 'b1', 'exited', '2026-09-26T00:00:00Z'), (2, 2, 'claude', '', '', '[]', '/w/2', 'b2', 'running', '2026-09-26T00:00:00Z')`,
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
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'attempt_status'`).Scan(&idx); err != nil || idx != 1 {
			t.Fatalf("attempt_status index = %d, %v", idx, err)
		}
		live, err := s.LiveAttempts(t.Context())
		if err != nil || len(live) != 1 || live[0].ID != 2 {
			t.Fatalf("LiveAttempts = %+v, %v", live, err)
		}
		for name, q := range map[string]string{
			"LiveAttempts":    attemptSelect + ` WHERE status IN (?, ?) ORDER BY id`,
			"Unwatched count": `SELECT (SELECT count(*) FROM attempt WHERE status IN (?, ?)), (SELECT count(*) FROM decision WHERE status = ?)`,
		} {
			args := []any{AttemptLaunching, AttemptRunning}
			if strings.HasPrefix(q, "SELECT (") {
				args = append(args, DecisionOpen)
			}
			rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
			if err != nil {
				t.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if p := strings.Join(plan, "\n"); !strings.Contains(p, "USING INDEX attempt_status") && !strings.Contains(p, "USING COVERING INDEX attempt_status") || strings.Contains(p, "SCAN attempt") {
				t.Fatalf("%s plan:\n%s", name, p)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
