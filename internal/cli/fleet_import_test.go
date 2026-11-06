package cli_test

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/state"
)

func importDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "hand.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func importExec(t *testing.T, home, q string, args ...any) {
	t.Helper()
	db := importDB(t, home)
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func importQuery(t *testing.T, home, q string, args ...any) string {
	t.Helper()
	var v string
	db := importDB(t, home)
	defer db.Close()
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return v
}

func importFixture(t *testing.T) *harnessFixture {
	t.Helper()
	h := newHarness(t)
	dest := h.home
	destID := field(h.ok("init", "--name", "dest"), "id")
	src := t.TempDir()
	srcID := field(h.ok("init", "--name", "src", src), "id")
	return &harnessFixture{h: h, src: src, dest: dest, srcID: srcID, destID: destID}
}

type harnessFixture struct {
	h             *harness
	src, dest     string
	srcID, destID string
}

func TestFleetImportCopiesProjectsTasksFilesAndReports(t *testing.T) {
	fx := importFixture(t)
	h := fx.h
	repo := gitRepo(t)
	extra := gitRepo(t)
	h.ok("project", "add", "shared", repo)
	h.ok("--home", fx.src, "project", "add", "shared", repo)
	h.ok("--home", fx.src, "project", "add", "extra", extra)
	h.ok("task", "add", "shared", "Destination task")
	h.ok("--home", fx.src, "task", "add", "shared", "Fix login with t2")
	h.ok("--home", fx.src, "task", "add", "shared", "See d5fe72c here")
	h.ok("--home", fx.src, "task", "add", "shared", "Ship t4-cont")
	write := func(home, rel, body string) {
		t.Helper()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(fx.src, "memory/operator.md", "source operator\n")
	write(fx.dest, "memory/operator.md", "dest operator\n")
	write(fx.src, "memory/projects/common.md", "source common\n")
	write(fx.dest, "memory/projects/common.md", "dest common\n")
	write(fx.src, "memory/projects/same.md", "same\n")
	write(fx.dest, "memory/projects/same.md", "same\n")
	write(fx.src, "deliverables/report.txt", "deliverable\n")
	write(fx.src, "plans/plan.md", "plan\n")
	write(fx.src, "briefs/brief.md", "brief\n")
	write(fx.src, "inbox/20261001-000000-1.txt", "inbox\n")
	importExec(t, fx.src, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES(1, 1, 'claude', 'sonnet', 'low', 'x', 'w', 'b', 'exited', '2026-10-06T00:00:00Z')`)
	importExec(t, fx.src, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES(1, 1, 1, 'done', 'the body', '2026-10-06T00:01:00Z')`)
	importExec(t, fx.src, `UPDATE task SET goal = 'After t3 ships, close t2.png and read path/t3', updated_at = '2026-01-02T03:04:05Z' WHERE id = 1`)

	before := importQuery(t, fx.dest, `SELECT count(*) || ' ' || coalesce(max(id), 0) FROM task`)
	out1, _, code := h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, out1)
	}
	out2, _, code := h.run("fleet", "import", fx.srcID, "INTO", fx.destID, "--dry-run")
	if code != 0 || out1 != out2 {
		t.Fatalf("dry runs differ: code=%d\n%s\n---\n%s", code, out1, out2)
	}
	for _, want := range []string{"dry_run: true", "task_offset: 1", "task_range: t2..t4", "reports_exported: 1", "task,t1,title,t2,t3"} {
		if !strings.Contains(out1, want) {
			t.Fatalf("dry run missing %q:\n%s", want, out1)
		}
	}
	hash1 := field(out1, "text_sha256")
	if len(hash1) != 64 || strings.Trim(hash1, "0123456789abcdef") != "" {
		t.Fatalf("dry run text hash = %q", hash1)
	}
	if _, err := os.Stat(filepath.Join(fx.dest, "hand.db.import.lock")); !os.IsNotExist(err) {
		t.Fatalf("dry run made the import lock: %v", err)
	}
	if after := importQuery(t, fx.dest, `SELECT count(*) || ' ' || coalesce(max(id), 0) FROM task`); after != before {
		t.Fatalf("dry run changed tasks: %s -> %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(fx.dest, "reports", "archive-"+fx.srcID)); !os.IsNotExist(err) {
		t.Fatalf("dry run made the archive directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); !os.IsNotExist(err) {
		t.Fatalf("dry run made the marker: %v", err)
	}
	importExec(t, fx.src, `UPDATE task SET title = 'See d5fe72c here now' WHERE id = 2`)
	out3, _, code := h.run("fleet", "import", fx.srcID, "INTO", fx.destID, "--dry-run")
	if code != 0 {
		t.Fatalf("third dry run: exit %d: %s", code, out3)
	}
	hash2 := field(out3, "text_sha256")
	if hash2 == hash1 {
		t.Fatalf("text hash did not change with the source text: %s", hash2)
	}

	out := h.ok("fleet", "import", fx.srcID, "INTO", fx.destID)
	if got := field(out, "text_sha256"); got != hash2 {
		t.Fatalf("real hash = %q, dry hash = %q", got, hash2)
	}
	collision := filepath.Join("memory", "projects", "common.md")
	for _, want := range []string{
		"task_offset: 1",
		"project,1,0",
		"task,3,1",
		"task,t1,title,t2,t3",
		"ambiguous[4]{table,key,column,token,context}:",
		"t2.png",
		"path/t3",
		"d5fe72c",
		"t4-cont",
		"reports_exported: 1",
		"copied,5",
		"same,1",
		"collision,1",
		"collisions[1]{source,destination,reason}:",
		collision,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("manifest missing %q:\n%s", want, out)
		}
	}
	if got := importQuery(t, fx.dest, `SELECT title FROM task WHERE id = 2`); got != "Fix login with t3" {
		t.Fatalf("rewritten title = %q", got)
	}
	if got := importQuery(t, fx.dest, `SELECT goal || ' ' || updated_at FROM task WHERE id = 2`); got != "After t4 ships, close t2.png and read path/t3 2026-01-02T03:04:05Z" {
		t.Fatalf("rewritten goal and timestamp = %q", got)
	}
	if got := importQuery(t, fx.dest, `SELECT title FROM task WHERE id = 4`); got != "Ship t4-cont" {
		t.Fatalf("ambiguous title = %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(fx.dest, "briefs"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o700 {
			t.Fatalf("new briefs directory mode = %o, want 700", perm)
		}
	}
	if got := readFileString(t, filepath.Join(fx.dest, "memory", "operator.md")); got != "dest operator\n" {
		t.Fatalf("destination operator = %q", got)
	}
	if got := readFileString(t, filepath.Join(fx.dest, "memory", "operator."+fx.srcID+".md")); got != "source operator\n" {
		t.Fatalf("source operator = %q", got)
	}
	if got := readFileString(t, filepath.Join(fx.dest, "memory", "projects", "common.md")); got != "dest common\n" {
		t.Fatalf("collision overwrote destination: %q", got)
	}
	if got := readFileString(t, filepath.Join(fx.src, "deliverables", "report.txt")); got != "deliverable\n" {
		t.Fatalf("source deliverable moved: %q", got)
	}
	archive := filepath.Join(fx.dest, "reports", "archive-"+fx.srcID, "r1-fix-login-with-t2.md")
	body := readFileString(t, archive)
	for _, want := range []string{"source: " + fx.srcID, "task: t2", "source_task: t1", "source_attempt: a1 (not imported)", "created: 2026-10-06T00:01:00Z", "the body"} {
		if !strings.Contains(body, want) {
			t.Fatalf("archive missing %q:\n%s", want, body)
		}
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != "1" {
		t.Fatalf("fleet.imported rows = %s", got)
	}
	if detail := importQuery(t, fx.dest, `SELECT detail FROM event WHERE kind = 'fleet.imported'`); !strings.Contains(detail, hash2) {
		t.Fatalf("event detail %q lacks the text hash %q", detail, hash2)
	}
	if markerBody := readFileString(t, filepath.Join(fx.src, "imported-into")); !strings.Contains(markerBody, hash2) {
		t.Fatalf("marker %q lacks the text hash %q", markerBody, hash2)
	}
	if err := os.Remove(filepath.Join(fx.src, "imported-into")); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 3 || !strings.Contains(errOut, "fleet.imported event") {
		t.Fatalf("dry rerun after removing the marker: code=%d stderr=%q", code, errOut)
	}
	before = importQuery(t, fx.dest, `SELECT count(*) || ' ' || coalesce(max(id), 0) FROM task`)
	_, errOut, code = h.run("fleet", "import", fx.srcID, "INTO", fx.destID)
	if code != 3 || !strings.Contains(errOut, "fleet.imported event") {
		t.Fatalf("rerun: code=%d stderr=%q", code, errOut)
	}
	if after := importQuery(t, fx.dest, `SELECT count(*) || ' ' || coalesce(max(id), 0) FROM task`); after != before {
		t.Fatalf("rerun changed tasks: %s -> %s", before, after)
	}
}

func TestFleetImportRefusesAProjectNameCollisionWithAnotherRepo(t *testing.T) {
	fx := importFixture(t)
	h := fx.h
	h.ok("project", "add", "clash", gitRepo(t))
	h.ok("--home", fx.src, "project", "add", "clash", gitRepo(t))
	out, errOut, code := h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 3 || !strings.Contains(errOut, "different repo") {
		t.Fatalf("collision: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); !os.IsNotExist(err) {
		t.Fatalf("marker written: %v", err)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != "0" {
		t.Fatalf("event written: %s", got)
	}
}

func TestFleetImportRefusesALiveAttempt(t *testing.T) {
	fx := importFixture(t)
	h := fx.h
	repo := gitRepo(t)
	h.ok("project", "add", "app", repo)
	h.ok("--home", fx.src, "project", "add", "app", repo)
	h.ok("--home", fx.src, "task", "add", "app", "Waiting task")
	importExec(t, fx.src, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES(1, 1, 'claude', 'sonnet', 'low', 'x', 'w', 'b', 'running', '2026-10-06T00:00:00Z')`)
	out, errOut, code := h.run("fleet", "import", fx.srcID, "INTO", fx.destID)
	if code != 3 || !strings.Contains(errOut, "live") || !strings.Contains(errOut, "a1") {
		t.Fatalf("live attempt: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); !os.IsNotExist(err) {
		t.Fatalf("marker written: %v", err)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "0" {
		t.Fatalf("tasks imported: %s", got)
	}
}

func TestFleetImportRefusesAStaleSchema(t *testing.T) {
	fx := importFixture(t)
	importExec(t, fx.src, `PRAGMA user_version = 17`)
	_, errOut, code := fx.h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 2 || !strings.Contains(errOut, "schema 17") {
		t.Fatalf("stale schema: code=%d stderr=%q", code, errOut)
	}
	if got := importQuery(t, fx.src, `PRAGMA user_version`); got != "17" {
		t.Fatalf("dry run migrated the source to %s", got)
	}
}

func TestFleetImportRefusesTheSameFleet(t *testing.T) {
	fx := importFixture(t)
	_, errOut, code := fx.h.run("fleet", "import", fx.srcID, "INTO", fx.srcID)
	if code != 3 || !strings.Contains(errOut, "same fleet") {
		t.Fatalf("same fleet: code=%d stderr=%q", code, errOut)
	}
}

func TestFleetImportRefusesWhenTheSourceMarkerExists(t *testing.T) {
	fx := importFixture(t)
	if err := os.WriteFile(filepath.Join(fx.src, "imported-into"), []byte("elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := fx.h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 3 || !strings.Contains(errOut, "marker") {
		t.Fatalf("marker: code=%d stderr=%q", code, errOut)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "0" {
		t.Fatalf("tasks imported: %s", got)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != "0" {
		t.Fatalf("event written: %s", got)
	}
}

func TestFleetImportDryRunOpensOnlyTheNamedFleets(t *testing.T) {
	fx := importFixture(t)
	third := t.TempDir()
	fx.h.ok("init", "--name", "third", third)
	for _, suffix := range []string{"-shm", "-wal"} {
		if err := os.Remove(filepath.Join(third, "hand.db"+suffix)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if _, errOut, code := fx.h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID); code != 0 {
		t.Fatalf("dry run: code=%d stderr=%q", code, errOut)
	}
	for _, suffix := range []string{"-shm", "-wal"} {
		if _, err := os.Stat(filepath.Join(third, "hand.db"+suffix)); !os.IsNotExist(err) {
			t.Fatalf("dry run opened the unrelated fleet: hand.db%s exists (%v)", suffix, err)
		}
	}
	if _, errOut, code := fx.h.run("fleet", "import", "--dry-run", "f000000000000", "INTO", fx.destID); code != 2 || !strings.Contains(errOut, `no fleet "f000000000000" is registered`) {
		t.Fatalf("unregistered id: code=%d stderr=%q", code, errOut)
	}
	for _, suffix := range []string{"-shm", "-wal"} {
		if _, err := os.Stat(filepath.Join(third, "hand.db"+suffix)); !os.IsNotExist(err) {
			t.Fatalf("an unregistered id opened the unrelated fleet: hand.db%s exists (%v)", suffix, err)
		}
	}
}

func TestFleetImportReusesAProjectSpelledRelativeOnTheDestination(t *testing.T) {
	fx := importFixture(t)
	repo := filepath.Join(fx.dest, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("project", "add", "shared", repo)
	fx.h.ok("--home", fx.src, "project", "add", "shared", repo)
	if got := importQuery(t, fx.dest, `SELECT repo FROM project WHERE name = 'shared'`); got != "repo" {
		t.Fatalf("destination repo = %q, want the relative spelling", got)
	}
	out, errOut, code := fx.h.run("fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID)
	if code != 0 {
		t.Fatalf("relative repo: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "shared,repo,shared,reused") || strings.Contains(errOut, "different repo") {
		t.Fatalf("relative repo was not reused:\n%s", out)
	}
}

func TestFleetImportHelpNamesTheGrammarAndDryRunCaveat(t *testing.T) {
	h := newHarness(t)
	out, _, code := h.run("fleet", "import", "--help")
	if code != 0 {
		t.Fatalf("help: exit %d: %s", code, out)
	}
	for _, want := range []string{"usage: hand fleet import SRC INTO DST [flags]", "hand.db-shm", "-dry-run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
}

type importRun struct {
	out, errOut string
	code        int
}

func runImportProcesses(t *testing.T, h *harness, cmds ...[]string) []importRun {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	results := make([]importRun, len(cmds))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, args := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(exe, args...)
			cmd.Env = append(os.Environ(), "HAND_TEST_AS_HAND=1", "SECONDHAND_HOME="+h.vars["SECONDHAND_HOME"])
			var out, errOut bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errOut
			<-start
			err := cmd.Run()
			code := 0
			if err != nil {
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Errorf("run %q: %v", args, err)
					code = -1
				} else {
					code = exit.ExitCode()
				}
			}
			results[i] = importRun{out.String(), errOut.String(), code}
		}()
	}
	close(start)
	wg.Wait()
	return results
}

func TestFleetImportConcurrentInvocationsImportOnce(t *testing.T) {
	fx := importFixture(t)
	h := fx.h
	repo := gitRepo(t)
	h.ok("project", "add", "app", repo)
	h.ok("--home", fx.src, "project", "add", "app", repo)
	h.ok("--home", fx.src, "task", "add", "app", "first")
	h.ok("--home", fx.src, "task", "add", "app", "second")
	dir := filepath.Join(fx.src, "deliverables")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 64 {
		name := filepath.Join(dir, "file-"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(name, bytes.Repeat([]byte("x"), 64*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmds := make([][]string, 4)
	for i := range cmds {
		cmds[i] = []string{"fleet", "import", fx.srcID, "INTO", fx.destID}
	}
	results := runImportProcesses(t, h, cmds...)
	won := 0
	for _, r := range results {
		if r.code == 0 {
			won++
			continue
		}
		if r.code != 3 || !(strings.Contains(r.errOut, "another fleet import") || strings.Contains(r.errOut, "already imported")) {
			t.Fatalf("concurrent run: code=%d out=%q stderr=%q", r.code, r.out, r.errOut)
		}
	}
	if won != 1 {
		t.Fatalf("%d concurrent runs imported, want 1", won)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "2" {
		t.Fatalf("tasks after concurrent imports = %s, want 2", got)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != "1" {
		t.Fatalf("fleet.imported rows = %s, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); err != nil {
		t.Fatalf("marker: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fx.dest, "deliverables"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 64 {
		t.Fatalf("deliverables = %d files, want 64", len(entries))
	}
}

func TestFleetImportRefusesAHeldImportLock(t *testing.T) {
	fx := importFixture(t)
	fx.h.ok("--home", fx.src, "project", "add", "app", gitRepo(t))
	fx.h.ok("--home", fx.src, "task", "add", "app", "held")
	lock, err := state.LockImport(fx.dest)
	if err != nil {
		t.Fatal(err)
	}
	runs := runImportProcesses(t, fx.h, []string{"fleet", "import", fx.srcID, "INTO", fx.destID})
	if runs[0].code != 3 || !strings.Contains(runs[0].errOut, "another fleet import is running in "+fx.dest) {
		t.Fatalf("held lock: code=%d out=%q stderr=%q", runs[0].code, runs[0].out, runs[0].errOut)
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); !os.IsNotExist(err) {
		t.Fatalf("a refused import wrote the marker: %v", err)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "0" {
		t.Fatalf("a refused import wrote %s tasks", got)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	runs = runImportProcesses(t, fx.h, []string{"fleet", "import", fx.srcID, "INTO", fx.destID})
	if runs[0].code != 0 {
		t.Fatalf("after release: code=%d out=%q stderr=%q", runs[0].code, runs[0].out, runs[0].errOut)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "1" {
		t.Fatalf("tasks after release = %s, want 1", got)
	}
}

func TestFleetImportTwoDestinationsImportTheSourceOnce(t *testing.T) {
	fx := importFixture(t)
	other := t.TempDir()
	otherID := field(fx.h.ok("init", "--name", "other", other), "id")
	fx.h.ok("--home", fx.src, "project", "add", "app", gitRepo(t))
	fx.h.ok("--home", fx.src, "task", "add", "app", "shared")
	cmds := [][]string{
		{"fleet", "import", fx.srcID, "INTO", fx.destID},
		{"fleet", "import", fx.srcID, "INTO", otherID},
	}
	hold, err := state.LockImport(fx.src)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runImportProcesses(t, fx.h, cmds...) {
		if r.code != 3 || !strings.Contains(r.errOut, "another fleet import is running in "+fx.src) {
			t.Fatalf("a held source lock let an import through: code=%d out=%q stderr=%q", r.code, r.out, r.errOut)
		}
	}
	for _, home := range []string{fx.dest, other} {
		if got := importQuery(t, home, `SELECT count(*) FROM task`); got != "0" {
			t.Fatalf("a refused import wrote %s tasks into %s", got, home)
		}
	}
	if err := hold.Close(); err != nil {
		t.Fatal(err)
	}
	won := -1
	for i, r := range runImportProcesses(t, fx.h, cmds...) {
		if r.code == 0 {
			if won >= 0 {
				t.Fatalf("two destinations imported one source: %q and %q", cmds[won], cmds[i])
			}
			won = i
			continue
		}
		if r.code != 3 || !(strings.Contains(r.errOut, "another fleet import") || strings.Contains(r.errOut, "already imported")) {
			t.Fatalf("concurrent destination: code=%d out=%q stderr=%q", r.code, r.out, r.errOut)
		}
	}
	if won < 0 {
		t.Fatalf("neither destination imported the source")
	}
	homes := []string{fx.dest, other}
	for i, home := range homes {
		want := "0"
		if i == won {
			want = "1"
		}
		if got := importQuery(t, home, `SELECT count(*) FROM task`); got != want {
			t.Fatalf("%s tasks = %s, want %s", home, got, want)
		}
		if got := importQuery(t, home, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != want {
			t.Fatalf("%s fleet.imported rows = %s, want %s", home, got, want)
		}
	}
	if body := readFileString(t, filepath.Join(fx.src, "imported-into")); !strings.Contains(body, []string{fx.destID, otherID}[won]) {
		t.Fatalf("marker %q does not name the destination that imported", body)
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestFleetImportFinishesTheMarkerAndFilesWhenStdoutFails(t *testing.T) {
	fx := importFixture(t)
	fx.h.ok("--home", fx.src, "project", "add", "app", gitRepo(t))
	fx.h.ok("--home", fx.src, "task", "add", "app", "one")
	if err := os.MkdirAll(filepath.Join(fx.src, "deliverables"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.src, "deliverables", "kept.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := cli.Run([]string{"fleet", "import", fx.srcID, "INTO", fx.destID}, fx.h.env(errorWriter{errors.New("stdout is full")}, &errOut))
	if code != 1 || !strings.Contains(errOut.String(), "stdout is full") {
		t.Fatalf("failing stdout: code=%d stderr=%q", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(fx.src, "imported-into")); err != nil {
		t.Fatalf("a failing stdout skipped the marker: %v", err)
	}
	if got := readFileString(t, filepath.Join(fx.dest, "deliverables", "kept.txt")); got != "kept\n" {
		t.Fatalf("a failing stdout skipped the files: %q", got)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM task`); got != "1" {
		t.Fatalf("tasks = %s, want 1", got)
	}
	if got := importQuery(t, fx.dest, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); got != "1" {
		t.Fatalf("fleet.imported rows = %s, want 1", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
