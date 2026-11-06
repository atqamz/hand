package cli_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/toon"
	_ "modernc.org/sqlite"
)

const moveAt = "2026-09-26T00:00:00Z"

func initMoveFleet(t *testing.T) (*harness, string, string) {
	t.Helper()
	h := newHarness(t)
	out := h.ok("init")
	home, id := field(out, "home"), field(out, "id")
	h.home = home
	return h, home, id
}

func openMoveDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "hand.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func moveExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func seedMoveFleet(t *testing.T, home, id string) {
	t.Helper()
	db := openMoveDB(t, home)
	defer db.Close()
	old := home
	tilde := "~/" + filepath.Base(home)
	moveExec(t, db, `INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, ?)`, old+"/repo", moveAt, `{"repo":"`+old+`"}`)
	moveExec(t, db, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'app', ?, ?, 'active', ?, ?)`,
		"Move "+old, "See "+old+"/a. Keep "+old+"x, pre"+old+", and "+tilde+"/ok", moveAt, moveAt)
	moveExec(t, db, `INSERT INTO plan(task_id, revision, body, created_at) VALUES (1, 1, ?, ?)`, "plan at "+old+"/p, done", moveAt)
	moveExec(t, db, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, ?, 'answered', ?, ?, ?, ?)`,
		"q "+old+"?", "a "+tilde+"/x and "+old, tilde, moveAt, moveAt)
	moveExec(t, db, `INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'test', 1, ?)`, moveAt, "e "+old+"/reports/t1/x.md. "+tilde)
	moveExec(t, db, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, screen, kept) VALUES (1, 1, 'claude', 'sonnet', 'low', ?, ?, ?, 'stopped', ?, ?, ?, ?)`,
		`["x","`+old+`/y","`+tilde+`"]`,
		"~/.secondhand/worktrees/"+id+"/t1-a1",
		"hand/"+id+"/t1-a1",
		old,
		moveAt,
		tilde+"/screen",
		old+" kept")
	moveExec(t, db, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', ?, ?)`,
		"Delivered: "+old+"/reports/t1/x.md. and "+old+"/y,", moveAt)
	moveExec(t, db, `INSERT INTO supervisor(id, harness, model, effort, argv, status, created_at) VALUES (1, 'claude', 'sonnet', 'low', ?, 'stopped', ?)`, `["`+old+`"]`, moveAt)
	moveExec(t, db, `INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, ?, ?, ?)`, tilde+" input", moveAt, moveAt)
	moveExec(t, db, `INSERT INTO timer(id, due_at, task_id, note, status, created_at) VALUES (1, ?, 1, ?, 'fired', ?)`, moveAt, old+" note", moveAt)
	moveExec(t, db, `INSERT INTO device(id, name, login, secret_hash, paired_at) VALUES (1, ?, ?, ?, ?)`, tilde, old, strings.Repeat("a", 64), moveAt)
	moveExec(t, db, `INSERT INTO project_grant(id, project, harness, pattern, created_at) VALUES (1, 'app', 'claude', ?, ?)`, old+"/bin", moveAt)
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestFleetMoveDryRunListsEveryCellAndWritesNothing(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	before := sha256File(t, filepath.Join(home, "hand.db"))
	names := dirNames(t, home)
	first := h.ok("fleet", "move", "--dry-run", dest)
	second := h.ok("fleet", "move", "--dry-run", dest)
	if first != second {
		t.Fatalf("dry runs differ:\n%s\n---\n%s", first, second)
	}
	for _, want := range []string{
		"move: dry-run",
		"fleet: " + filepath.Base(home),
		"id: " + id,
		"from: " + toon.Value(home),
		"to: " + toon.Value(dest),
		"absolute: 17",
		"tilde: 8",
		"cells[20]{table,column,ref,absolute,tilde}:",
		"attempt,argv,a1,1,1",
		"attempt,screen,a1,0,1",
		"decision,answer,d1,1,1",
		"event,detail,e2,1,1",
		"report,body,r1,2,0",
		"task,goal,t1,1,1",
		"project,settings,app,1,0",
		"Run `hand fleet move --yes " + dest + "`",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("dry run missing %q:\n%s", want, first)
		}
	}
	for _, unwanted := range []string{"attempt,worktree", "attempt,branch", "fleet,name"} {
		if strings.Contains(first, unwanted) {
			t.Fatalf("dry run lists %q:\n%s", unwanted, first)
		}
	}
	if after := sha256File(t, filepath.Join(home, "hand.db")); after != before {
		t.Fatalf("the dry run changed hand.db: %s -> %s", before, after)
	}
	if got := dirNames(t, home); strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("the dry run changed the home: %q -> %q", names, got)
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the dry run created the destination: %v", err)
	}
}

func TestFleetMoveRewritesTheDatabaseThenMovesTheFolder(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	newTilde := "~/" + filepath.Base(dest)
	out := h.ok("fleet", "move", "--yes", dest)
	for _, want := range []string{
		"move: moved",
		"from: " + toon.Value(home),
		"to: " + toon.Value(dest),
		"before,17,8,20",
		"after,0,0,0",
		"verification: 0",
		"foreign_key_check: 0",
		"integrity_check: ok",
		"rows: ok",
		"home: " + toon.Value(dest),
		"registry: " + toon.Value(filepath.Join(h.vars["SECONDHAND_HOME"], "fleets", id)),
		"Run `hand init` in " + dest,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("move missing %q:\n%s", want, out)
		}
	}
	backup := field(out, "backup")
	if !strings.HasPrefix(backup, dest+string(filepath.Separator)) {
		t.Fatalf("backup = %q", backup)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old home still there: %v", err)
	}
	link, err := os.ReadFile(filepath.Join(h.vars["SECONDHAND_HOME"], "fleets", id))
	if err != nil || strings.TrimSpace(string(link)) != dest {
		t.Fatalf("registry = %q, %v", link, err)
	}
	db := openMoveDB(t, dest)
	defer db.Close()
	read := func(q string) string {
		t.Helper()
		var v string
		if err := db.QueryRow(q).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ q, want string }{
		{`SELECT body FROM report WHERE id = 1`, "Delivered: " + dest + "/reports/t1/x.md. and " + dest + "/y,"},
		{`SELECT goal FROM task WHERE id = 1`, "See " + dest + "/a. Keep " + home + "x, pre" + home + ", and " + newTilde + "/ok"},
		{`SELECT argv FROM attempt WHERE id = 1`, `["x","` + dest + `/y","` + newTilde + `"]`},
		{`SELECT branch FROM attempt WHERE id = 1`, "hand/" + id + "/t1-a1"},
		{`SELECT worktree FROM attempt WHERE id = 1`, "~/.secondhand/worktrees/" + id + "/t1-a1"},
		{`SELECT detail FROM event WHERE kind = 'test'`, "e " + dest + "/reports/t1/x.md. " + newTilde},
		{`SELECT detail FROM event WHERE kind = 'fleet.moved'`, home + " -> " + dest},
	} {
		if got := read(c.q); got != c.want {
			t.Fatalf("%s = %q, want %q", c.q, got, c.want)
		}
	}
	h.home = dest
	if orient := h.ok("orient"); !strings.Contains(orient, "home: "+toon.Value(dest)) {
		t.Fatalf("orient = %q", orient)
	}
	if show := h.ok("task", "show", "t1"); !strings.Contains(field(show, "goal"), "See "+dest+"/a.") {
		t.Fatalf("task show = %q", show)
	}
	if show := h.ok("report", "show", "r1"); !strings.Contains(field(show, "body"), "Delivered: "+dest+"/reports/t1/x.md.") {
		t.Fatalf("report show = %q", show)
	}
	_, errOut, code := h.run("fleet", "move", "--yes", dest)
	if code != 3 || !strings.Contains(errOut, "already this fleet's home") {
		t.Fatalf("second move: code=%d stderr=%q", code, errOut)
	}
}

func TestFleetMoveUsesTheGivenHomeOutsideTheFleet(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	h.cwd = t.TempDir()
	dest := filepath.Join(t.TempDir(), "moved")
	out := h.ok("--home", home, "fleet", "move", "--dry-run", dest)
	if field(out, "from") != home || field(out, "to") != dest {
		t.Fatalf("dry run = %q", out)
	}
}

func TestFleetMoveRefusesLiveWork(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup string
		want  string
	}{
		{"attempt", `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (2, 1, 'claude', 'sonnet', 'low', '[]', '', '', 'running', '` + moveAt + `')`, "a2 is running on t1"},
		{"timer", `INSERT INTO timer(id, due_at, note, status, created_at) VALUES (3, '` + moveAt + `', '', 'armed', '` + moveAt + `')`, "w3 is armed"},
		{"input", `INSERT INTO supervisor_input(id, body, created_at) VALUES (4, 'hello', '` + moveAt + `')`, "1 supervisor input(s) are pending delivery"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, home, id := initMoveFleet(t)
			seedMoveFleet(t, home, id)
			db := openMoveDB(t, home)
			moveExec(t, db, c.setup)
			db.Close()
			before := sha256File(t, filepath.Join(home, "hand.db"))
			dest := filepath.Join(t.TempDir(), "moved")
			_, errOut, code := h.run("fleet", "move", "--yes", dest)
			if code != 3 || !strings.Contains(errOut, c.want) {
				t.Fatalf("code=%d stderr=%q", code, errOut)
			}
			if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the refused move created %s", dest)
			}
			if after := sha256File(t, filepath.Join(home, "hand.db")); after != before {
				t.Fatalf("the refused move changed hand.db: %s -> %s", before, after)
			}
		})
	}
}

func TestFleetMoveRefusesBadDestinationsAndMissingFlags(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no flags", []string{"fleet", "move", filepath.Join(t.TempDir(), "moved")}, 2, "pass --dry-run"},
		{"both flags", []string{"fleet", "move", "--dry-run", "--yes", filepath.Join(t.TempDir(), "moved")}, 2, "pass --dry-run"},
		{"same path", []string{"fleet", "move", "--yes", home}, 3, "already this fleet's home"},
		{"nested", []string{"fleet", "move", "--yes", filepath.Join(home, "inside")}, 3, "nested"},
		{"not empty", []string{"fleet", "move", "--yes", occupied}, 3, "exists and is not empty"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, errOut, code := h.run(c.args...)
			if code != c.code || !strings.Contains(errOut, c.want) {
				t.Fatalf("code=%d stderr=%q, want %d and %q", code, errOut, c.code, c.want)
			}
		})
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("a refused move removed the home: %v", err)
	}
}

func TestFleetMoveRefusesAFleetMissingFromTheRegistry(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	if err := os.Remove(filepath.Join(h.vars["SECONDHAND_HOME"], "fleets", id)); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := h.run("fleet", "move", "--yes", filepath.Join(t.TempDir(), "moved"))
	if code != 2 || !strings.Contains(errOut, "is registered") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestFleetMoveRefusesAnotherFilesystem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no portable second volume")
	}
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	parent := otherFilesystem(t)
	if parent == "" {
		t.Skip("no second filesystem to test")
	}
	dest := filepath.Join(parent, "hand-move-dest")
	before := sha256File(t, filepath.Join(home, "hand.db"))
	_, errOut, code := h.run("fleet", "move", "--yes", dest)
	if code != 3 || !strings.Contains(errOut, "different filesystems") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if after := sha256File(t, filepath.Join(home, "hand.db")); after != before {
		t.Fatalf("the refused move changed hand.db")
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the refused move created %s", dest)
	}
}

func otherFilesystem(t *testing.T) string {
	t.Helper()
	here, err := os.MkdirTemp("", "hand-move-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(here) })
	for _, dir := range []string{"/dev/shm", "/run", "/var/tmp", os.TempDir(), "/tmp"} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		same, err := fsx.SameDevice(here, dir)
		if err != nil || same {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, ".hand-move-probe-"+filepath.Base(here)), nil, 0o600); err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(dir, ".hand-move-probe-"+filepath.Base(here)))
		return dir
	}
	return ""
}

func TestFleetMoveOnACopiedFleetKeepsReadsWorking(t *testing.T) {
	h, home, id := initMoveFleet(t)
	db := openMoveDB(t, home)
	moveExec(t, db, `INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, '')`, filepath.Join(home, "repo"), moveAt)
	moveExec(t, db, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'app', 'Copied task', 'Read me', 'active', ?, ?)`, moveAt, moveAt)
	moveExec(t, db, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '[]', '', '', 'stopped', ?)`, moveAt)
	moveExec(t, db, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', 'Delivered: `+filepath.Join(home, "reports", "t1", "x.md")+`', ?)`, moveAt)
	db.Close()
	copied := filepath.Join(t.TempDir(), "copy")
	copyMoveTree(t, home, copied)
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "fleets", id), []byte(copied+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.home = copied
	dest := filepath.Join(t.TempDir(), "moved")
	h.ok("fleet", "move", "--yes", dest)
	h.home = dest
	if out := h.ok("orient"); !strings.Contains(out, "home: "+toon.Value(dest)) {
		t.Fatalf("orient = %q", out)
	}
	if out := h.ok("task", "show", "t1"); !strings.Contains(out, "Copied task") || !strings.Contains(out, "Read me") {
		t.Fatalf("task show = %q", out)
	}
	if out := h.ok("report", "show", "r1"); !strings.Contains(out, "Delivered:") {
		t.Fatalf("report show = %q", out)
	}
}

func copyMoveTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
