package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	argv, _ := json.Marshal([]string{"x", old + "/y"})
	exec(`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, screen, kept) VALUES (1, 1, 'claude', 'sonnet', 'low', ?, ?, ?, 'stopped', ?, ?, ?, ?)`,
		string(argv)+" "+tilde,
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

func setUserHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

func openAt(t *testing.T, home string) *Store {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(home, "hand.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMoveHomeRewritesEveryTextColumnAndKeepsBoundaries(t *testing.T) {
	parent := t.TempDir()
	setUserHome(t, parent)
	old := filepath.Join(parent, "fleet-hand")
	s := openAt(t, old)
	to := filepath.Join(parent, "fleet-name")
	tilde := "~/" + filepath.Base(old)
	moveFixture(t, s, old, tilde)
	ctx := context.Background()
	f, err := s.Fleet(ctx)
	if err != nil || f.Home != old {
		t.Fatalf("recorded home = %q, %v; want %q", f.Home, err, old)
	}
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 16 || rep.Tilde != 8 || len(rep.Cells) != 19 || len(rep.Hits) != 0 {
		t.Fatalf("counts = %d absolute, %d tilde, %d cells, %d hits; want 16, 8, 19, 0: %+v", rep.Absolute, rep.Tilde, len(rep.Cells), len(rep.Hits), rep.Cells)
	}
	if rep.AfterAbsolute != 0 || rep.ForeignKeys != 0 || rep.Integrity != "ok" || rep.MarkerSeq == 0 || rep.EventSeq != 0 {
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
	wantArgv, _ := json.Marshal([]string{"x", to + "/y"})
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
		{`SELECT argv FROM attempt WHERE id = 1`, string(wantArgv) + " " + newTilde},
		{`SELECT reason FROM attempt WHERE id = 1`, to},
		{`SELECT screen FROM attempt WHERE id = 1`, newTilde + "/screen"},
		{`SELECT kept FROM attempt WHERE id = 1`, old + " kept"},
		{`SELECT body FROM report WHERE id = 1`, "Delivered: " + to + "/reports/t1/x.md. and " + to + "/y,"},
		{`SELECT argv FROM supervisor WHERE id = 1`, `["` + to + `"]`},
		{`SELECT body FROM supervisor_input WHERE id = 1`, tilde + " input"},
		{`SELECT note FROM timer WHERE id = 1`, old + " note"},
		{`SELECT name FROM device WHERE id = 1`, newTilde},
		{`SELECT login FROM device WHERE id = 1`, to},
		{`SELECT pattern FROM project_grant WHERE id = 1`, to + "/bin"},
		{`SELECT name FROM fleet`, "fleet"},
		{`SELECT home FROM fleet`, to},
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
	if got := read(`SELECT detail FROM event WHERE kind = 'fleet.moving'`); got != moveDetail(old, to) {
		t.Fatalf("marker = %q", got)
	}
	from, markerTo, pending, err := s.PendingMove(ctx)
	if err != nil || !pending || from != old || markerTo != to {
		t.Fatalf("pending = %q, %q, %v, %v", from, markerTo, pending, err)
	}
	seq, err := s.ConfirmMove(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if seq == 0 {
		t.Fatal("no fleet.moved event")
	}
	if again, err := s.ConfirmMove(ctx, old, to); err != nil || again != seq {
		t.Fatalf("second confirm = %d, %v; want %d", again, err, seq)
	}
	if _, _, pending, err := s.PendingMove(ctx); err != nil || pending {
		t.Fatalf("still pending: %v, %v", pending, err)
	}
	if got := read(`SELECT detail FROM event WHERE kind = 'fleet.moved'`); got != moveDetail(old, to) {
		t.Fatalf("move event = %q", got)
	}
}

func TestMoveHomeRollsBackACellAddedAfterTheScan(t *testing.T) {
	s, _ := openTest(t)
	old := s.home
	to := filepath.Join(t.TempDir(), "new")
	moveFixture(t, s, old, "~/.secondhand")
	ctx := context.Background()
	_, err := s.moveHome(ctx, old, to, false, func(tx moveWriter) error {
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event WHERE kind IN ('fleet.moved', 'fleet.moving')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("move event count = %d", n)
	}
}

func TestReplacePathBoundaries(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "a")
	old := filepath.Join(home, "fleet")
	to := filepath.Join(home, "new")
	cases := []struct {
		in       string
		want     string
		absolute int
		tilde    int
	}{
		{old, to, 1, 0},
		{old + "/reports/x.md", to + "/reports/x.md", 1, 0},
		{old + ".", to + ".", 1, 0},
		{old + ". ", to + ". ", 1, 0},
		{old + ".bak", old + ".bak", 0, 0},
		{old + ".old/x", old + ".old/x", 0, 0},
		{old + "./x", old + "./x", 0, 0},
		{old + ".1", old + ".1", 0, 0},
		{old + ".12:00", old + ".12:00", 0, 0},
		{old + " ", old + " ", 0, 0},
		{old + "+", old + "+", 0, 0},
		{old + "é", old + "é", 0, 0},
		{old + ",", to + ",", 1, 0},
		{old + "-old", old + "-old", 0, 0},
		{old + "x", old + "x", 0, 0},
		{old + "old", old + "old", 0, 0},
		{"pre" + old, "pre" + old, 0, 0},
		{"~/" + filepath.Base(old), "~/" + filepath.Base(to), 0, 1},
		{"~/" + filepath.Base(old) + "/x", "~/" + filepath.Base(to) + "/x", 0, 1},
		{"~/" + filepath.Base(old) + ".", "~/" + filepath.Base(to) + ".", 0, 1},
		{"~/" + filepath.Base(old) + ".bak", "~/" + filepath.Base(old) + ".bak", 0, 0},
		{"~/" + filepath.Base(old) + "-old", "~/" + filepath.Base(old) + "-old", 0, 0},
		{"~/" + filepath.Base(old) + " ", "~/" + filepath.Base(old) + " ", 0, 0},
		{"~/" + filepath.Base(old) + "+", "~/" + filepath.Base(old) + "+", 0, 0},
		{"~/" + filepath.Base(old) + "é", "~/" + filepath.Base(old) + "é", 0, 0},
		{"pre~/" + filepath.Base(old), "pre~/" + filepath.Base(old), 0, 0},
		{`~\/` + filepath.Base(old) + `/x`, `~\/` + filepath.Base(to) + `/x`, 0, 1},
		{`~\u002f` + filepath.Base(old) + `\u002fx`, `~\u002f` + filepath.Base(to) + `\u002fx`, 0, 1},
		{`~\u002F` + filepath.Base(old) + `\u002Fx`, `~\u002F` + filepath.Base(to) + `\u002Fx`, 0, 1},
		{`\/home\/a\/fleet\/x`, `\/home\/a\/new\/x`, 1, 0},
		{`\u002fhome\u002fa\u002ffleet\u002fx`, `\u002fhome\u002fa\u002fnew\u002fx`, 1, 0},
		{`file:///home/a/fleet/x`, `file:///home/a/new/x`, 1, 0},
	}
	for _, c := range cases {
		got, absolute, tilde := rewriteHome(c.in, old, to, home)
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
	if _, err := s.db.ExecContext(ctx, `UPDATE fleet SET home = ?`, from); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, `["`+escapedFrom+`\\reports"]`); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 2 || rep.Tilde != 0 {
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

func TestMoveHomeRewritesTheSameBasenameShorthandToTheAbsoluteDestination(t *testing.T) {
	parent := t.TempDir()
	setUserHome(t, parent)
	old := filepath.Join(parent, "fleet-hand")
	s := openAt(t, old)
	to := filepath.Join(t.TempDir(), filepath.Base(old))
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, "~/"+filepath.Base(old)+"/x "+old); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 2 || rep.Tilde != 1 {
		t.Fatalf("counts = %d, %d", rep.Absolute, rep.Tilde)
	}
	var detail string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != to+"/x "+to {
		t.Fatalf("detail = %q", detail)
	}
}

func TestMoveHomeRewritesTheShorthandWhenBothAreUnderHome(t *testing.T) {
	parent := t.TempDir()
	setUserHome(t, parent)
	old := filepath.Join(parent, "fleet")
	s := openAt(t, old)
	to := filepath.Join(parent, "renamed")
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, "~/fleet/x "+old); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Absolute != 2 || rep.Tilde != 1 {
		t.Fatalf("counts = %d, %d", rep.Absolute, rep.Tilde)
	}
	var detail string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "~/renamed/x "+to {
		t.Fatalf("detail = %q", detail)
	}
}

func TestMoveHomeRefusesTheShorthandWhenTheOldHomeIsNotUnderHome(t *testing.T) {
	setUserHome(t, t.TempDir())
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	old := s.home
	to := filepath.Join(t.TempDir(), "new")
	detail := "~/" + filepath.Base(old) + "/x " + old
	if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, detail); err != nil {
		t.Fatal(err)
	}
	_, err := s.MoveHome(ctx, old, to)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "verification found 1 old home path(s)") || !strings.Contains(err.Error(), "event.detail") || !strings.Contains(err.Error(), "edit them to name "+to) {
		t.Fatalf("err = %v", err)
	}
	if rep, err := s.PlanHome(ctx, old, to); err != nil || len(rep.Hits) != 1 || rep.Tilde != 0 || rep.Absolute != 2 {
		t.Fatalf("plan = %+v, %v", rep, err)
	}
	var got, home string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != detail {
		t.Fatalf("the move committed: detail = %q", got)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT home FROM fleet`).Scan(&home); err != nil {
		t.Fatal(err)
	}
	if home != old {
		t.Fatalf("home = %q", home)
	}
}

func TestMoveHomeAcceptsADestinationWithNameBytes(t *testing.T) {
	for _, suffix := range []string{" 2026", "+2026", "é", "."} {
		t.Run(strconv.Quote(suffix), func(t *testing.T) {
			parent := t.TempDir()
			setUserHome(t, parent)
			old := filepath.Join(parent, "fleet-hand")
			s := openAt(t, old)
			to := filepath.Join(parent, filepath.Base(old)+suffix)
			ctx := context.Background()
			if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
				t.Fatal(err)
			}
			sibling := "see " + old + " backup and " + old + "+old and " + old + "é and " + old + ".bak"
			if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, sibling); err != nil {
				t.Fatal(err)
			}
			rep, err := s.MoveHome(ctx, old, to)
			if err != nil {
				t.Fatal(err)
			}
			if rep.AfterAbsolute != 0 || len(rep.Hits) != 0 {
				t.Fatalf("verification = %d, hits = %+v", rep.AfterAbsolute, rep.Hits)
			}
			var detail, recorded string
			if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&detail); err != nil {
				t.Fatal(err)
			}
			if detail != sibling {
				t.Fatalf("a sibling with name bytes was rewritten: %q", detail)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT home FROM fleet`).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded != to {
				t.Fatalf("home = %q, want %q", recorded, to)
			}
		})
	}
}

func TestMoveHomeSurvivesArrowsInAPath(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	old := s.home
	to := filepath.Join(t.TempDir(), "a -> b")
	if _, err := s.MoveHome(ctx, old, to); err != nil {
		t.Fatal(err)
	}
	from, gotTo, pending, err := s.PendingMove(ctx)
	if err != nil || !pending || from != old || gotTo != to {
		t.Fatalf("pending = %q, %q, %v, %v; want %q, %q", from, gotTo, pending, err, old, to)
	}
	if _, err := s.ConfirmMove(ctx, old, to); err != nil {
		t.Fatal(err)
	}
	if _, _, pending, err := s.PendingMove(ctx); err != nil || pending {
		t.Fatalf("still pending: %v, %v", pending, err)
	}
}

func TestMoveHomeRewritesFormsBeyondThePlainPath(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		run  func(t *testing.T, s *Store, old, to string) (string, string)
	}{
		{"file url", func(t *testing.T, s *Store, old, to string) (string, string) {
			return "file://" + old + "/x", "file://" + to + "/x"
		}},
		{"json slash", func(t *testing.T, s *Store, old, to string) (string, string) {
			return strings.ReplaceAll(filepath.ToSlash(old), "/", `\u002f`) + `\u002fx`,
				strings.ReplaceAll(filepath.ToSlash(to), "/", `\u002f`) + `\u002fx`
		}},
		{"json escaped slash", func(t *testing.T, s *Store, old, to string) (string, string) {
			return strings.ReplaceAll(filepath.ToSlash(old), "/", `\/`) + `\/x`,
				strings.ReplaceAll(filepath.ToSlash(to), "/", `\/`) + `\/x`
		}},
		{"case variant", func(t *testing.T, s *Store, old, to string) (string, string) {
			if runtime.GOOS != "windows" {
				t.Skip("case variants only name the home on Windows")
			}
			return strings.ToUpper(old) + "/x", to + "/x"
		}},
		{"symlinked home", func(t *testing.T, s *Store, old, to string) (string, string) {
			if runtime.GOOS == "windows" {
				t.Skip("symlink creation needs a privilege")
			}
			raw := t.TempDir()
			if err := os.Symlink(filepath.Dir(old), filepath.Join(raw, "link")); err != nil {
				t.Fatal(err)
			}
			setUserHome(t, filepath.Join(raw, "link"))
			return filepath.Join(raw, "link", filepath.Base(old), "x"), to + "/x"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := openTest(t)
			if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
				t.Fatal(err)
			}
			old := s.home
			to := filepath.Join(t.TempDir(), "new")
			detail, want := c.run(t, s, old, to)
			if _, err := s.db.ExecContext(ctx, `INSERT INTO event(at, kind, detail) VALUES ('2026-09-26T00:00:00Z', 'test', ?)`, detail); err != nil {
				t.Fatal(err)
			}
			rep, err := s.MoveHome(ctx, old, to)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Absolute != 2 || rep.Tilde != 0 || len(rep.Hits) != 0 {
				t.Fatalf("counts = %d absolute, %d tilde, %d hits; cells = %+v", rep.Absolute, rep.Tilde, len(rep.Hits), rep.Cells)
			}
			var got string
			if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'test'`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("detail = %q, want %q", got, want)
			}
		})
	}
}

func TestMoveHomeKeepsFleetMovedRows(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	old := s.home
	mid := filepath.Join(t.TempDir(), "mid")
	final := filepath.Join(t.TempDir(), "final")
	if _, err := s.RewriteHome(ctx, old, mid); err != nil {
		t.Fatal(err)
	}
	rep, err := s.MoveHome(ctx, mid, final)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Cells {
		if c.Table == "event" && c.Column == "detail" {
			t.Fatalf("the move rewrote an event detail: %+v", c)
		}
	}
	var first string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'fleet.moved' ORDER BY seq LIMIT 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first != moveDetail(old, mid) {
		t.Fatalf("history = %q", first)
	}
	if _, err := s.ConfirmMove(ctx, mid, final); err != nil {
		t.Fatal(err)
	}
	var last string
	if err := s.db.QueryRowContext(ctx, `SELECT detail FROM event WHERE kind = 'fleet.moved' ORDER BY seq DESC LIMIT 1`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != moveDetail(mid, final) {
		t.Fatalf("last = %q", last)
	}
}

func TestMoveHomeRefusesAFromThatTheDatabaseDoesNotRecord(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	_, err := s.MoveHome(ctx, filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(t.TempDir(), "new"))
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "records this fleet's home") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenReadWorksOnAPreHomeSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{schema, migrations[0], migrations[1], `PRAGMA user_version = 18`, `INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`} {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, err := s.Fleet(context.Background())
	if err != nil || f.Home != "" || f.Name != "old" {
		t.Fatalf("fleet = %+v, %v", f, err)
	}
	if _, err := s.PlanHome(context.Background(), "/old", "/new"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadRefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = OpenRead(path)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "newer than this hand") {
		t.Fatalf("OpenRead = %v", err)
	}
}

func TestMoveHomeConfirmMoveNeedsAPendingMarker(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmMove(ctx, s.home, filepath.Join(t.TempDir(), "new")); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "no move is pending") {
		t.Fatalf("err = %v", err)
	}
}

func TestMoveHomeEventSeqIsNotTheMarker(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.CreateFleet(ctx, "fleet"); err != nil {
		t.Fatal(err)
	}
	old := s.home
	to := filepath.Join(t.TempDir(), "new")
	rep, err := s.MoveHome(ctx, old, to)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := s.db.QueryRowContext(ctx, `SELECT seq FROM event WHERE kind = 'fleet.moving'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if rep.MarkerSeq != seq {
		t.Fatalf("marker = %d, want %d", rep.MarkerSeq, seq)
	}
	if got := strconv.FormatInt(rep.EventSeq, 10); got != "0" {
		t.Fatalf("event = %s", got)
	}
}
