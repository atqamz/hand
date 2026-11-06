package state

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func moveFixture(t *testing.T, s *Store, old, tilde string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	at := "2026-09-26T00:00:00Z"
	exec(`INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, ?)`, old+"/repo", at, `{"repo":"`+old+`"}`)
	exec(`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'app', ?, ?, 'active', ?, ?)`,
		"Move "+old, "See "+old+"/a. Keep "+old+"x, pre"+old+", and "+tilde+"/ok", at, at)
	exec(`INSERT INTO plan(task_id, revision, body, created_at) VALUES (1, 1, ?, ?)`, "plan at "+old+"/p, done", at)
	exec(`INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, ?, 'answered', ?, ?, ?, ?)`,
		"q "+old+"?", "a "+tilde+"/x and "+old, tilde, at, at)
	exec(`INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'test', 1, ?)`, at, "e "+old+"/reports/t1/x.md. "+tilde)
	exec(`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, screen, kept) VALUES (1, 1, 'claude', 'sonnet', 'low', ?, ?, ?, 'stopped', ?, ?, ?, ?)`,
		`["x","`+old+`/y"] `+tilde,
		"~/.secondhand/worktrees/f000000000000/t1-a1",
		"hand/f000000000000/t1-a1",
		old,
		at,
		tilde+"/screen",
		old+" kept")
	exec(`INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', ?, ?)`,
		"Delivered: "+old+"/reports/t1/x.md. and "+old+"/y,", at)
	exec(`INSERT INTO supervisor(id, harness, model, effort, argv, status, created_at) VALUES (1, 'claude', 'sonnet', 'low', ?, 'stopped', ?)`, `["`+old+`"]`, at)
	exec(`INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, ?, ?, ?)`, tilde+" input", at, at)
	exec(`INSERT INTO timer(id, due_at, task_id, note, status, created_at) VALUES (1, ?, 1, ?, 'fired', ?)`, at, old+" note", at)
	hash := tilde + "/" + strings.Repeat("a", 64-len(tilde)-1)
	exec(`INSERT INTO device(id, name, login, secret_hash, paired_at) VALUES (1, ?, ?, ?, ?)`, tilde, old, hash, at)
	exec(`INSERT INTO project_grant(id, project, harness, pattern, created_at) VALUES (1, 'app', 'claude', ?, ?)`, old+"/bin", at)
}

func TestMoveHomeRewritesEveryTextColumnAndKeepsBoundaries(t *testing.T) {
	s, _ := openTest(t)
	old := s.home
	to := filepath.Join(t.TempDir(), "new")
	tilde := "~/" + filepath.Base(old)
	moveFixture(t, s, old, tilde)
	ctx := context.Background()
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 17 || rep.Tilde != 9 || len(rep.Cells) != 21 {
		t.Fatalf("counts = %d absolute, %d tilde, %d cells; want 17, 9, 21: %+v", rep.Absolute, rep.Tilde, len(rep.Cells), rep.Cells)
	}
	if rep.AfterAbsolute != 0 || rep.AfterTilde != 0 || rep.ForeignKeys != 0 || rep.Integrity != "ok" {
		t.Fatalf("checks = %+v", rep)
	}
	newTilde := "~/" + filepath.Base(to)
	read := func(q string) string {
		t.Helper()
		var v string
		if err := s.db.QueryRowContext(ctx, q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct{ q, want string }{
		{`SELECT repo FROM project WHERE name = 'app'`, to + "/repo"},
		{`SELECT settings FROM project WHERE name = 'app'`, `{"repo":"` + to + `"}`},
		{`SELECT title FROM task WHERE id = 1`, "Move " + to},
		{`SELECT goal FROM task WHERE id = 1`, "See " + to + "/a. Keep " + old + "x, pre" + old + ", and " + newTilde + "/ok"},
		{`SELECT body FROM plan WHERE task_id = 1`, "plan at " + to + "/p, done"},
		{`SELECT question FROM decision WHERE id = 1`, "q " + to + "?"},
		{`SELECT answer FROM decision WHERE id = 1`, "a " + newTilde + "/x and " + to},
		{`SELECT answered_by FROM decision WHERE id = 1`, newTilde},
		{`SELECT detail FROM event WHERE kind = 'test'`, "e " + to + "/reports/t1/x.md. " + newTilde},
		{`SELECT argv FROM attempt WHERE id = 1`, `["x","` + to + `/y"] ` + newTilde},
		{`SELECT reason FROM attempt WHERE id = 1`, to},
		{`SELECT screen FROM attempt WHERE id = 1`, newTilde + "/screen"},
		{`SELECT kept FROM attempt WHERE id = 1`, to + " kept"},
		{`SELECT body FROM report WHERE id = 1`, "Delivered: " + to + "/reports/t1/x.md. and " + to + "/y,"},
		{`SELECT argv FROM supervisor WHERE id = 1`, `["` + to + `"]`},
		{`SELECT body FROM supervisor_input WHERE id = 1`, newTilde + " input"},
		{`SELECT note FROM timer WHERE id = 1`, to + " note"},
		{`SELECT name FROM device WHERE id = 1`, newTilde},
		{`SELECT login FROM device WHERE id = 1`, to},
		{`SELECT pattern FROM project_grant WHERE id = 1`, to + "/bin"},
		{`SELECT name FROM fleet`, "fleet"},
	}
	for _, c := range cases {
		if got := read(c.q); got != c.want {
			t.Fatalf("%s = %q, want %q", c.q, got, c.want)
		}
	}
	if got := read(`SELECT branch FROM attempt WHERE id = 1`); got != "hand/f000000000000/t1-a1" {
		t.Fatalf("branch = %q", got)
	}
	if got := read(`SELECT worktree FROM attempt WHERE id = 1`); got != "~/.secondhand/worktrees/f000000000000/t1-a1" {
		t.Fatalf("worktree = %q", got)
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT secret_hash FROM device WHERE id = 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 || !strings.HasPrefix(hash, newTilde) {
		t.Fatalf("secret_hash = %q", hash)
	}
	if rep.EventSeq == 0 {
		t.Fatal("no fleet.moved event")
	}
	if got := read(`SELECT detail FROM event WHERE seq = ` + strconv.FormatInt(rep.EventSeq, 10)); got != old+" -> "+to {
		t.Fatalf("move event = %q", got)
	}
}

func TestMoveHomeRollsBackACellAddedAfterTheScan(t *testing.T) {
	s, _ := openTest(t)
	old := s.home
	to := filepath.Join(t.TempDir(), "new")
	moveFixture(t, s, old, "~/"+filepath.Base(old))
	ctx := context.Background()
	_, err := s.moveHome(ctx, old, to, func(tx moveWriter) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'late', ?)`, old+"/late")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "verification found") || !strings.Contains(err.Error(), "event.detail") {
		t.Fatalf("err = %v", err)
	}
	var body string
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM report WHERE id = 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, old) {
		t.Fatalf("the move committed anyway: report body = %q", body)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event WHERE kind = 'fleet.moved'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("move event count = %d", n)
	}
}

func TestReplacePathBoundaries(t *testing.T) {
	old := filepath.Join(string(filepath.Separator), "home", "a", "fleet")
	to := filepath.Join(string(filepath.Separator), "home", "a", "new")
	cases := []struct {
		in       string
		want     string
		absolute int
		tilde    int
	}{
		{old, to, 1, 0},
		{old + "/reports/x.md", to + "/reports/x.md", 1, 0},
		{old + ".", to + ".", 1, 0},
		{old + ",", to + ",", 1, 0},
		{old + "-old", old + "-old", 0, 0},
		{old + "x", old + "x", 0, 0},
		{"pre" + old, "pre" + old, 0, 0},
		{"~/" + filepath.Base(old), "~/" + filepath.Base(to), 0, 1},
		{"~/" + filepath.Base(old) + "/x", "~/" + filepath.Base(to) + "/x", 0, 1},
		{"~/" + filepath.Base(old) + ".", "~/" + filepath.Base(to) + ".", 0, 1},
		{"~/" + filepath.Base(old) + "-old", "~/" + filepath.Base(old) + "-old", 0, 0},
		{"pre~/" + filepath.Base(old), "pre~/" + filepath.Base(old), 0, 0},
	}
	for _, c := range cases {
		got, absolute, tilde := rewriteHome(c.in, old, to)
		if got != c.want || absolute != c.absolute || tilde != c.tilde {
			t.Fatalf("rewriteHome(%q) = %q, %d, %d; want %q, %d, %d", c.in, got, absolute, tilde, c.want, c.absolute, c.tilde)
		}
	}
}

func TestReplacePathFindsJSONEscapedBackslashes(t *testing.T) {
	from := `C:\Users\a\fleet-hand`
	to := `C:\Users\a\fleet-personal`
	escapedFrom := strings.ReplaceAll(from, `\`, `\\`)
	escapedTo := strings.ReplaceAll(to, `\`, `\\`)
	in := `["--dir","` + escapedFrom + `\\reports","` + escapedFrom + `x"]`
	want := `["--dir","` + escapedTo + `\\reports","` + escapedFrom + `x"]`
	got, n := replacePath(in, escapedFrom, escapedTo)
	if got != want || n != 1 {
		t.Fatalf("replacePath = %q, %d; want %q, 1", got, n, want)
	}
}

func TestMoveHomeRewritesAJSONEscapedBackslashPath(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	from := "/home/a/fleet" + `\` + "hand"
	to := "/home/a/fleet" + `\` + "personal"
	escapedFrom := strings.ReplaceAll(from, `\`, `\\`)
	escapedTo := strings.ReplaceAll(to, `\`, `\\`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, `["`+escapedFrom+`\\reports"]`); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 1 || rep.Tilde != 0 {
		t.Fatalf("counts = %d, %d", rep.Absolute, rep.Tilde)
	}
	var detail string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != `["`+escapedTo+`\\reports"]` {
		t.Fatalf("detail = %q", detail)
	}
}

func TestMoveHomeKeepsTheShorthandWhenTheBaseNameStays(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	old := s.home
	to := filepath.Join(t.TempDir(), filepath.Base(old))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, "~/"+filepath.Base(old)+"/x "+old); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 1 || rep.Tilde != 0 {
		t.Fatalf("counts = %d, %d", rep.Absolute, rep.Tilde)
	}
	var detail string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "~/"+filepath.Base(old)+"/x "+to {
		t.Fatalf("detail = %q", detail)
	}
}
