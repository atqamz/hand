package cli_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
	_ "modernc.org/sqlite"
)

const moveAt = "2026-09-26T00:00:00Z"

func initMoveFleet(t *testing.T) (*harness, string, string) {
	t.Helper()
	h := newHarness(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", parent)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", parent)
	}
	home := filepath.Join(parent, "fleet-hand")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	h.cwd = home
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

func moveEventDetail(t *testing.T, from, to string) string {
	t.Helper()
	b, err := json.Marshal(struct {
		From string `json:"from"`
		To   string `json:"to"`
	}{from, to})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func seedMoveFleet(t *testing.T, home, id string) {
	t.Helper()
	db := openMoveDB(t, home)
	defer db.Close()
	old := home
	tilde := "~/" + filepath.Base(home)
	settings, _ := json.Marshal(map[string]string{"repo": old})
	argv, _ := json.Marshal([]string{"x", old + "/y", tilde})
	supArgv, _ := json.Marshal([]string{old})
	moveExec(t, db, `INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, ?)`, old+"/repo", moveAt, string(settings))
	moveExec(t, db, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'app', ?, ?, 'active', ?, ?)`,
		"Move "+old, "See "+old+"/a. Keep "+old+"x, pre"+old+", and "+tilde+"/ok", moveAt, moveAt)
	moveExec(t, db, `INSERT INTO plan(task_id, revision, body, created_at) VALUES (1, 1, ?, ?)`, "plan at "+old+"/p, done", moveAt)
	moveExec(t, db, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, ?, 'answered', ?, ?, ?, ?)`,
		"q "+old+"?", "a "+tilde+"/x and "+old, tilde, moveAt, moveAt)
	moveExec(t, db, `INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'test', 1, ?)`, moveAt, "e "+old+"/reports/t1/x.md. "+tilde)
	moveExec(t, db, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, screen, kept) VALUES (1, 1, 'claude', 'sonnet', 'low', ?, ?, ?, 'stopped', ?, ?, ?, ?)`,
		string(argv),
		"~/.secondhand/worktrees/"+id+"/t1-a1",
		"hand/"+id+"/t1-a1",
		old,
		moveAt,
		tilde+"/screen",
		old+" kept")
	moveExec(t, db, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', ?, ?)`,
		"Delivered: "+old+"/reports/t1/x.md. and "+old+"/y,", moveAt)
	moveExec(t, db, `INSERT INTO supervisor(id, harness, model, effort, argv, status, created_at) VALUES (1, 'claude', 'sonnet', 'low', ?, 'stopped', ?)`, string(supArgv), moveAt)
	moveExec(t, db, `INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, ?, ?, ?)`, tilde+" input", moveAt, moveAt)
	moveExec(t, db, `INSERT INTO timer(id, due_at, task_id, note, status, created_at) VALUES (1, ?, 1, ?, 'fired', ?)`, moveAt, old+" note", moveAt)
	moveExec(t, db, `INSERT INTO device(id, name, login, secret_hash, paired_at) VALUES (1, 'phone', ?, ?, ?)`, tilde, strings.Repeat("a", 64), moveAt)
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

func moveFieldPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

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
		"fleet: " + filepath.Base(dest),
		"id: " + id,
		"from: " + toon.Value(home),
		"to: " + toon.Value(dest),
		"absolute: 15",
		"tilde: 7",
		"cells[17]{table,column,ref,absolute,tilde}:",
		"attempt,argv,a1,1,1",
		"attempt,screen,a1,0,1",
		"decision,answer,d1,1,1",
		"event,detail,e2,1,1",
		"fleet,home,fleet,1,0",
		"report,body,r1,2,0",
		"task,goal,t1,1,1",
		"project,settings,app,1,0",
		"verification: 0",
		"Run `hand fleet move --yes '" + dest + "'`",
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
	var kept []string
	for _, name := range dirNames(t, home) {
		if name != "hand.db-wal" && name != "hand.db-shm" {
			kept = append(kept, name)
		}
	}
	if strings.Join(kept, ",") != strings.Join(names, ",") {
		t.Fatalf("the dry run changed the home: %q -> %q", names, kept)
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the dry run created the destination: %v", err)
	}
}

func TestFleetMoveRewritesTheDatabaseThenMovesTheFolder(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	newHome := dest
	out := h.ok("fleet", "move", "--yes", dest)
	for _, want := range []string{
		"move: moved",
		"from: " + toon.Value(home),
		"to: " + toon.Value(dest),
		"before,15,7,17",
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
	pre, err := sql.Open("sqlite", "file:"+backup+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer pre.Close()
	var backupHome, backupBody string
	if err := pre.QueryRow(`SELECT home FROM fleet`).Scan(&backupHome); err != nil || backupHome != home {
		t.Fatalf("backup home = %q, %v; want %q", backupHome, err, home)
	}
	if err := pre.QueryRow(`SELECT body FROM report WHERE id = 1`).Scan(&backupBody); err != nil || !strings.Contains(backupBody, home) {
		t.Fatalf("backup body = %q, %v; want the old home", backupBody, err)
	}
	var backupEvents int
	if err := pre.QueryRow(`SELECT COUNT(*) FROM event WHERE kind = 'fleet.moved'`).Scan(&backupEvents); err != nil || backupEvents != 0 {
		t.Fatalf("backup moved events = %d, %v", backupEvents, err)
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
	wantArgv, _ := json.Marshal([]string{"x", dest + "/y", newHome})
	wantSettings, _ := json.Marshal(map[string]string{"repo": dest})
	wantSupArgv, _ := json.Marshal([]string{dest})
	for _, c := range []struct{ q, want string }{
		{`SELECT body FROM report WHERE id = 1`, "Delivered: " + dest + "/reports/t1/x.md. and " + dest + "/y,"},
		{`SELECT goal FROM task WHERE id = 1`, "See " + dest + "/a. Keep " + home + "x, pre" + home + ", and " + newHome + "/ok"},
		{`SELECT argv FROM attempt WHERE id = 1`, string(wantArgv)},
		{`SELECT settings FROM project WHERE name = 'app'`, string(wantSettings)},
		{`SELECT argv FROM supervisor WHERE id = 1`, string(wantSupArgv)},
		{`SELECT branch FROM attempt WHERE id = 1`, "hand/" + id + "/t1-a1"},
		{`SELECT worktree FROM attempt WHERE id = 1`, "~/.secondhand/worktrees/" + id + "/t1-a1"},
		{`SELECT detail FROM event WHERE kind = 'test'`, "e " + dest + "/reports/t1/x.md. " + newHome},
		{`SELECT detail FROM event WHERE kind = 'fleet.moved'`, moveEventDetail(t, home, dest)},
		{`SELECT home FROM fleet`, dest},
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

func TestFleetMoveFollowsTheFolderName(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "fleet-personal")
	out := h.ok("fleet", "move", "--yes", dest)
	if !strings.Contains(out, "fleet: fleet-personal") {
		t.Fatalf("move = %q", out)
	}
	db := openMoveDB(t, dest)
	defer db.Close()
	var name, detail string
	if err := db.QueryRow(`SELECT name FROM fleet`).Scan(&name); err != nil || name != "fleet-personal" {
		t.Fatalf("name = %q, %v; want fleet-personal", name, err)
	}
	if err := db.QueryRow(`SELECT detail FROM event WHERE kind = 'fleet.renamed'`).Scan(&detail); err != nil || detail != id+" fleet-personal" {
		t.Fatalf("rename = %q, %v; want %q", detail, err, id+" fleet-personal")
	}
}

func TestFleetMoveKeepsACustomFleetName(t *testing.T) {
	h, home, id := initMoveFleet(t)
	if out := h.ok("init", "--name", "Yes 2 Games"); !strings.Contains(out, "fleet: Yes 2 Games") {
		t.Fatalf("rename = %q", out)
	}
	seedMoveFleet(t, home, id)
	before := openMoveDB(t, home)
	var lastSeq int64
	if err := before.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM event`).Scan(&lastSeq); err != nil {
		t.Fatal(err)
	}
	before.Close()
	dest := filepath.Join(t.TempDir(), "fleet-personal")
	out := h.ok("fleet", "move", "--yes", dest)
	if !strings.Contains(out, "fleet: Yes 2 Games") {
		t.Fatalf("move = %q", out)
	}
	db := openMoveDB(t, dest)
	defer db.Close()
	var name string
	if err := db.QueryRow(`SELECT name FROM fleet`).Scan(&name); err != nil || name != "Yes 2 Games" {
		t.Fatalf("name = %q, %v; want Yes 2 Games", name, err)
	}
	var renamed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event WHERE kind = 'fleet.renamed' AND seq > ?`, lastSeq).Scan(&renamed); err != nil || renamed != 0 {
		t.Fatalf("renamed events = %d, %v; want 0", renamed, err)
	}
}

func TestFleetMoveRewriteOnlyFollowsTheFolderName(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	copied := filepath.Join(t.TempDir(), "fleet-personal")
	if err := os.Rename(home, copied); err != nil {
		t.Fatal(err)
	}
	h.home, h.cwd = copied, copied
	if out := h.ok("init"); !strings.Contains(out, "moved_from: "+toon.Value(home)) {
		t.Fatalf("init = %q", out)
	}
	out := h.ok("fleet", "move", "--rewrite-only", "--yes", "--from", home)
	for _, want := range []string{"move: rewritten", "fleet: fleet-personal", "home: " + toon.Value(copied)} {
		if !strings.Contains(out, want) {
			t.Fatalf("rewrite missing %q:\n%s", want, out)
		}
	}
	db := openMoveDB(t, copied)
	defer db.Close()
	var name, detail string
	if err := db.QueryRow(`SELECT name FROM fleet`).Scan(&name); err != nil || name != "fleet-personal" {
		t.Fatalf("name = %q, %v; want fleet-personal", name, err)
	}
	if err := db.QueryRow(`SELECT detail FROM event WHERE kind = 'fleet.renamed'`).Scan(&detail); err != nil || detail != id+" fleet-personal" {
		t.Fatalf("rename = %q, %v; want %q", detail, err, id+" fleet-personal")
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
	h, home, _ := initMoveFleet(t)
	db := openMoveDB(t, home)
	moveExec(t, db, `INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, '')`, filepath.Join(home, "repo"), moveAt)
	moveExec(t, db, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'app', 'Copied task', 'Read me', 'active', ?, ?)`, moveAt, moveAt)
	moveExec(t, db, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '[]', '', '', 'stopped', ?)`, moveAt)
	moveExec(t, db, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', ?, ?)`, "Delivered: "+filepath.Join(home, "reports", "t1", "x.md"), moveAt)
	db.Close()
	copied := filepath.Join(t.TempDir(), "copy")
	copyMoveTree(t, home, copied)
	h.cwd, h.home = copied, copied
	if _, errOut, code := h.run("init"); code != 3 || !strings.Contains(errOut, "is also at "+home) {
		t.Fatalf("init with the original present: code=%d stderr=%q", code, errOut)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("init"); !strings.Contains(out, "moved_from: "+toon.Value(home)) {
		t.Fatalf("init = %q", out)
	}
	dest := filepath.Join(t.TempDir(), "moved")
	if _, errOut, code := h.run("fleet", "move", "--yes", dest); code != 3 || !strings.Contains(errOut, "rewrite-only --yes --from "+home) {
		t.Fatalf("move without --from: code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("orient"); !strings.Contains(out, "--rewrite-only --yes --from '"+moveFieldPath(home)+"'") {
		t.Fatalf("orient = %q", out)
	}
	if dry := h.ok("fleet", "move", "--dry-run", "--rewrite-only", "--from", home); !strings.Contains(dry, "Run `hand fleet move --rewrite-only --yes --from '"+home+"'` here") {
		t.Fatalf("rewrite-only dry run = %q", dry)
	}
	out := h.ok("fleet", "move", "--rewrite-only", "--yes", "--from", home)
	for _, want := range []string{"move: rewritten", "from: " + toon.Value(home), "to: " + toon.Value(copied), "verification: 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rewrite missing %q:\n%s", want, out)
		}
	}
	if out := h.ok("fleet", "move", "--yes", dest); !strings.Contains(out, "move: moved") {
		t.Fatalf("later move = %q", out)
	}
	h.home = dest
	if out := h.ok("orient"); !strings.Contains(out, "home: "+toon.Value(dest)) {
		t.Fatalf("orient = %q", out)
	}
	if out := h.ok("task", "show", "t1"); !strings.Contains(out, "Copied task") || !strings.Contains(out, "Read me") {
		t.Fatalf("task show = %q", out)
	}
	if out := h.ok("report", "show", "r1"); !strings.Contains(field(out, "body"), "Delivered: "+filepath.Join(dest, "reports", "t1", "x.md")) {
		t.Fatalf("report show = %q", out)
	}
	db = openMoveDB(t, dest)
	defer db.Close()
	var body, recorded string
	if err := db.QueryRow(`SELECT body FROM report WHERE id = 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, home) || strings.Contains(body, copied) {
		t.Fatalf("report body still names a previous home: %q", body)
	}
	if err := db.QueryRow(`SELECT home FROM fleet`).Scan(&recorded); err != nil || recorded != dest {
		t.Fatalf("recorded home = %q, %v; want %q", recorded, err, dest)
	}
}

func TestFleetMoveRequiresFromWhenNoHomeIsRecorded(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	db := openMoveDB(t, home)
	moveExec(t, db, `UPDATE fleet SET home = ''`)
	db.Close()
	dest := filepath.Join(t.TempDir(), "moved")
	if _, errOut, code := h.run("fleet", "move", "--dry-run", dest); code != 3 || !strings.Contains(errOut, "records no home") || !strings.Contains(errOut, "--from "+home) {
		t.Fatalf("move without --from: code=%d stderr=%q", code, errOut)
	}
	out := h.ok("fleet", "move", "--dry-run", "--from", home, dest)
	if !strings.Contains(out, "move: dry-run") || !strings.Contains(out, "verification: 0") || !strings.Contains(out, "cells[16]") {
		t.Fatalf("dry run with --from = %q", out)
	}
}

func TestFleetMoveToADirectChildOfHomeKeepsTheShorthand(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(filepath.Dir(home), "hand-renamed")
	out := h.ok("fleet", "move", "--yes", dest)
	if !strings.Contains(out, "move: moved") || !strings.Contains(out, "before,15,7,17") || !strings.Contains(out, "verification: 0") {
		t.Fatalf("move = %q", out)
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
	if got := read(`SELECT screen FROM attempt WHERE id = 1`); got != "~/hand-renamed/screen" {
		t.Fatalf("screen = %q", got)
	}
	if got := read(`SELECT login FROM device WHERE id = 1`); got != "~/hand-renamed" {
		t.Fatalf("device name = %q", got)
	}
	if got := read(`SELECT body FROM supervisor_input WHERE id = 1`); got != "~/"+filepath.Base(home)+" input" {
		t.Fatalf("input = %q", got)
	}
	if got := read(`SELECT body FROM report WHERE id = 1`); got != "Delivered: "+dest+"/reports/t1/x.md. and "+dest+"/y," {
		t.Fatalf("report = %q", got)
	}
	if got := read(`SELECT goal FROM task WHERE id = 1`); !strings.Contains(got, "~/hand-renamed/ok") {
		t.Fatalf("goal = %q", got)
	}
	if got := read(`SELECT home FROM fleet`); got != dest {
		t.Fatalf("home = %q", got)
	}
}

func TestFleetMoveToASameBasenameUnderHomeRewritesToTheAbsoluteDestination(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(filepath.Dir(home), "archive", filepath.Base(home))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	dry := h.ok("fleet", "move", "--dry-run", dest)
	for _, want := range []string{"tilde: 7", "verification: 0", "cells[17]"} {
		if !strings.Contains(dry, want) {
			t.Fatalf("dry run missing %q:\n%s", want, dry)
		}
	}
	if strings.Contains(dry, "shorthand") {
		t.Fatalf("dry run lists shorthand:\n%s", dry)
	}
	out := h.ok("fleet", "move", "--yes", dest)
	if !strings.Contains(out, "move: moved") || !strings.Contains(out, "verification: 0") {
		t.Fatalf("move = %q", out)
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
		{`SELECT screen FROM attempt WHERE id = 1`, dest + "/screen"},
		{`SELECT login FROM device WHERE id = 1`, dest},
		{`SELECT answered_by FROM decision WHERE id = 1`, dest},
		{`SELECT body FROM supervisor_input WHERE id = 1`, "~/fleet-hand input"},
		{`SELECT body FROM report WHERE id = 1`, "Delivered: " + dest + "/reports/t1/x.md. and " + dest + "/y,"},
	} {
		if got := read(c.q); got != c.want {
			t.Fatalf("%s = %q, want %q", c.q, got, c.want)
		}
	}
	if got := read(`SELECT goal FROM task WHERE id = 1`); strings.Contains(got, "~/fleet-hand") || !strings.Contains(got, dest+"/ok") {
		t.Fatalf("goal = %q", got)
	}
}

func TestFleetMoveResumesAPendingMove(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	st, err := state.Open(filepath.Join(home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.MoveHome(context.Background(), home, dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if rep.MarkerSeq == 0 || rep.EventSeq != 0 {
		t.Fatalf("marker = %d, event = %d", rep.MarkerSeq, rep.EventSeq)
	}
	if out := h.ok("fleet", "move", "--dry-run", dest); !strings.Contains(out, "move: pending") {
		t.Fatalf("dry run = %q", out)
	}
	if out := h.ok("orient"); !strings.Contains(out, "interrupted: the database names") || !strings.Contains(out, "hand fleet move --yes '"+moveFieldPath(dest)+"'") {
		t.Fatalf("orient = %q", out)
	}
	if out := h.ok("fleet", "move", "--yes", dest); !strings.Contains(out, "move: resumed") || !strings.Contains(out, "verification: 0") {
		t.Fatalf("resume = %q", out)
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
	var moved, moving int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event WHERE kind = 'fleet.moved'`).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("moved events = %d, %v", moved, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM event WHERE kind = 'fleet.moving'`).Scan(&moving); err != nil || moving != 1 {
		t.Fatalf("moving events = %d, %v", moving, err)
	}
	var detail string
	if err := db.QueryRow(`SELECT detail FROM event WHERE kind = 'fleet.moved'`).Scan(&detail); err != nil || detail != moveEventDetail(t, home, dest) {
		t.Fatalf("moved detail = %q, %v", detail, err)
	}
}

func TestFleetMoveFinishesAfterTheFolderMoved(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "moved")
	st, err := state.Open(filepath.Join(home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveHome(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(home, dest); err != nil {
		t.Fatal(err)
	}
	h.home = dest
	h.cwd = dest
	if _, errOut, code := h.run("orient"); code != 3 || !strings.Contains(errOut, "a move from "+home+" to "+dest+" is pending") || !strings.Contains(errOut, "hand fleet move --yes "+dest+"` here") {
		t.Fatalf("orient before init: code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("init"); !strings.Contains(out, "moved_from: "+toon.Value(home)) {
		t.Fatalf("init = %q", out)
	}
	if out := h.ok("orient"); !strings.Contains(out, "interrupted: the database names") {
		t.Fatalf("orient after init = %q", out)
	}
	other := filepath.Join(t.TempDir(), "other")
	if _, errOut, code := h.run("fleet", "move", "--yes", other); code != 3 || !strings.Contains(errOut, "is pending and this folder is already") {
		t.Fatalf("new destination: code=%d stderr=%q", code, errOut)
	}
	out := h.ok("fleet", "move", "--yes", dest)
	if !strings.Contains(out, "move: finished") || !strings.Contains(out, "verification: 0") {
		t.Fatalf("finish = %q", out)
	}
	link, err := os.ReadFile(filepath.Join(h.vars["SECONDHAND_HOME"], "fleets", id))
	if err != nil || strings.TrimSpace(string(link)) != dest {
		t.Fatalf("registry = %q, %v", link, err)
	}
	db := openMoveDB(t, dest)
	defer db.Close()
	var moved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM event WHERE kind = 'fleet.moved'`).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("moved events = %d, %v", moved, err)
	}
}

func TestFleetMoveRefusesALiveWatcherAndBoard(t *testing.T) {
	for _, c := range []struct {
		name string
		path func(*harness, string) string
		want string
	}{
		{"watcher", func(_ *harness, home string) string { return filepath.Join(home, "watch.pid") }, "the watcher is running"},
		{"board", func(h *harness, _ string) string { return filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid") }, "the board is running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, home, id := initMoveFleet(t)
			seedMoveFleet(t, home, id)
			forget, err := update.WritePID(c.path(h, home))
			if err != nil {
				t.Fatal(err)
			}
			defer forget()
			_, errOut, code := h.run("fleet", "move", "--yes", filepath.Join(t.TempDir(), "moved"))
			if code != 3 || !strings.Contains(errOut, c.want) {
				t.Fatalf("code=%d stderr=%q", code, errOut)
			}
		})
	}
}

func TestFleetMoveRewritesAFileURL(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	db := openMoveDB(t, home)
	moveExec(t, db, `UPDATE report SET body = ? WHERE id = 1`, "file://"+filepath.Join(home, "reports", "t1", "x.md"))
	db.Close()
	dest := filepath.Join(t.TempDir(), "moved")
	out := h.ok("fleet", "move", "--dry-run", dest)
	if !strings.Contains(out, "verification: 0") || strings.Contains(out, "remaining[") {
		t.Fatalf("dry run = %q", out)
	}
	if out := h.ok("fleet", "move", "--yes", dest); !strings.Contains(out, "move: moved") || !strings.Contains(out, "verification: 0") {
		t.Fatalf("move = %q", out)
	}
	db = openMoveDB(t, dest)
	defer db.Close()
	var body string
	if err := db.QueryRow(`SELECT body FROM report WHERE id = 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if want := "file://" + filepath.Join(dest, "reports", "t1", "x.md"); body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestFleetMoveRefusesShorthandWhenHomeIsElsewhere(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	other := t.TempDir()
	t.Setenv("HOME", other)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", other)
	}
	dest := filepath.Join(t.TempDir(), "moved")
	dry := h.ok("fleet", "move", "--dry-run", dest)
	if !strings.Contains(dry, "verification: 7") || !strings.Contains(dry, "remaining[7]{table,column,ref}:") {
		t.Fatalf("dry run = %q", dry)
	}
	_, errOut, code := h.run("fleet", "move", "--yes", dest)
	if code != 3 || !strings.Contains(errOut, "verification found 7 old home path(s)") || !strings.Contains(errOut, "edit them to name "+dest) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(dest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the refused move created %s", dest)
	}
	db := openMoveDB(t, home)
	defer db.Close()
	var screen string
	if err := db.QueryRow(`SELECT screen FROM attempt WHERE id = 1`).Scan(&screen); err != nil {
		t.Fatal(err)
	}
	if screen != "~/"+filepath.Base(home)+"/screen" {
		t.Fatalf("screen = %q", screen)
	}
}

func TestFleetMoveQuotesTheDestinationInHelp(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "new folder")
	out := h.ok("fleet", "move", "--dry-run", dest)
	if !strings.Contains(out, "--yes '"+dest+"'") {
		t.Fatalf("help does not quote the path:\n%s", out)
	}
}

func TestOrientQuotesTheInterruptedMoveDestination(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	dest := filepath.Join(t.TempDir(), "new folder")
	st, err := state.Open(filepath.Join(home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveHome(context.Background(), home, dest); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	out := h.ok("orient")
	if !strings.Contains(out, "hand fleet move --yes '"+moveFieldPath(dest)+"'") || strings.Contains(out, "--yes "+moveFieldPath(dest)) {
		t.Fatalf("orient = %q", out)
	}
}

func TestOrientReportsAnUnreadablePendingMove(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	db := openMoveDB(t, home)
	moveExec(t, db, `INSERT INTO event(at, kind, detail) VALUES (?, 'fleet.moving', 'not a move')`, moveAt)
	db.Close()
	out := h.ok("orient")
	if !strings.Contains(out, "the pending move is unreadable") || !strings.Contains(out, "fleet.moving") {
		t.Fatalf("orient = %q", out)
	}
}

func TestFleetMoveKeepsAnEmptyDestinationWhenTheRewriteFails(t *testing.T) {
	h, home, id := initMoveFleet(t)
	seedMoveFleet(t, home, id)
	other := t.TempDir()
	t.Setenv("HOME", other)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", other)
	}
	dest := filepath.Join(t.TempDir(), "moved")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("fleet", "move", "--yes", dest); code != 3 || !strings.Contains(errOut, "verification found") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		t.Fatalf("the refused move removed the empty destination: %v", err)
	}
	if entries, err := os.ReadDir(dest); err != nil || len(entries) != 0 {
		t.Fatalf("the refused move wrote into the destination: %v, %v", entries, err)
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
