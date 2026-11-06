package cli_test

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	_ "modernc.org/sqlite"
)

type mergePair struct {
	h            *harness
	src, dst     string
	srcID, dstID string
}

func TestFleetMergeDryRunIsIdenticalAndARealRunCarriesEverything(t *testing.T) {
	p := newMergePair(t)
	beforeSrc, err := state.DatabaseHash(filepath.Join(p.src, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	beforeDst, err := state.DatabaseHash(filepath.Join(p.dst, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}

	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--dry-run", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "project web exists on both sides") {
		t.Fatalf("collision: code=%d stderr=%q", code, errOut)
	}
	_, errOut, code = p.h.run("--home", p.dst, "fleet", "merge", "--dry-run", "--rename-project", "web=srcweb", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "armed timer") {
		t.Fatalf("timer: code=%d stderr=%q", code, errOut)
	}

	args := []string{"--home", p.dst, "fleet", "merge", "--dry-run", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID}
	first := p.h.ok(args...)
	second := p.h.ok(args...)
	if first != second {
		t.Fatalf("dry runs differ:\n--- first\n%s\n--- second\n%s", first, second)
	}
	for _, want := range []string{
		"dry_run: true",
		"task,2,3,5,3",
		"attempt,2,1,3,1",
		"report,1,1,2,1",
		"decision,1,1,2,1",
		"timer,1,1,2,1",
		"supervisor_input,1,1,2,1",
		"t,",
		"a,",
		"r,",
		"d,",
		"w,",
		"i,",
		"url,1",
		"home,",
		"ambiguous[1]{table,key,column,refs}:",
		"event,",
		"a1b2c3d",
		"  memory,",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("dry run missing %q:\n%s", want, first)
		}
	}
	afterSrc, err := state.DatabaseHash(filepath.Join(p.src, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	afterDst, err := state.DatabaseHash(filepath.Join(p.dst, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	if beforeSrc != afterSrc || beforeDst != afterDst {
		t.Fatalf("dry run changed a database: src %s/%s dst %s/%s", beforeSrc, afterSrc, beforeDst, afterDst)
	}
	if _, err := os.Stat(filepath.Join(p.dst, "reports", "t1", "report.md")); !os.IsNotExist(err) {
		t.Fatalf("dry run moved a file: %v", err)
	}

	real := p.h.ok("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if field(real, "text_sha256") != field(first, "text_sha256") {
		t.Fatalf("real text differs from the dry run:\n--- dry\n%s\n--- real\n%s", first, real)
	}
	if field(real, "merged") != p.dstID+" hand" || field(real, "source") != p.srcID+" personal" {
		t.Fatalf("manifest identity = %q / %q", field(real, "merged"), field(real, "source"))
	}

	src, dst := openMergeDB(t, p.src), openMergeDB(t, p.dst)
	defer src.Close()
	defer dst.Close()
	assertCount(t, dst, `SELECT COUNT(*) FROM task`, 5)
	assertCount(t, dst, `SELECT COUNT(*) FROM attempt`, 3)
	assertCount(t, dst, `SELECT COUNT(*) FROM report`, 2)
	assertCount(t, dst, `SELECT COUNT(*) FROM decision`, 2)
	assertCount(t, dst, `SELECT COUNT(*) FROM timer`, 2)
	assertCount(t, dst, `SELECT COUNT(*) FROM supervisor_input`, 2)
	assertCount(t, dst, `SELECT COUNT(*) FROM event`, 8)
	assertCount(t, dst, `SELECT COUNT(*) FROM project`, 4)
	assertCount(t, dst, `SELECT COUNT(*) FROM supervisor`, 1)
	assertCount(t, dst, `SELECT COUNT(*) FROM device`, 0)
	assertCount(t, dst, `SELECT COUNT(*) FROM project WHERE name = 'srcweb' AND repo = `+quote(p.dst+"/projects/web"), 1)

	var title, goal string
	if err := dst.QueryRow(`SELECT title, goal FROM task WHERE id = 4`).Scan(&title, &goal); err != nil {
		t.Fatal(err)
	}
	if title != "Fix t4 with a2 and r2, see d2 and w2 and i2" {
		t.Fatalf("title = %q", title)
	}
	for _, kept := range []string{"hand/" + p.srcID + "/t1-a1", "reports/t1", "briefs/t1", "shots/t1"} {
		if !strings.Contains(goal, kept) {
			t.Fatalf("goal lost protected %q: %q", kept, goal)
		}
	}
	if !strings.Contains(goal, "home "+p.dst) || strings.Contains(goal, p.src+"/") {
		t.Fatalf("goal home paths = %q", goal)
	}
	var argv string
	if err := dst.QueryRow(`SELECT argv FROM attempt WHERE id = 2`).Scan(&argv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(argv, "hand/"+p.srcID+"/t1-a1") || !strings.Contains(argv, "t4") || !strings.Contains(argv, "a2") || strings.Contains(argv, p.src) {
		t.Fatalf("argv = %q", argv)
	}
	var status, note, ended string
	if err := dst.QueryRow(`SELECT status, note, ended_at FROM timer WHERE id = 2`).Scan(&status, &note, &ended); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || !strings.Contains(note, "w2") || ended == "" {
		t.Fatalf("timer = %q %q %q", status, note, ended)
	}
	assertCount(t, dst, `SELECT COUNT(*) FROM event WHERE kind = 'timer.cancelled' AND detail = 'w2'`, 1)
	assertCount(t, dst, `SELECT COUNT(*) FROM event WHERE kind = 'fleet.merged' AND detail LIKE '`+p.srcID+` personal:%'`, 1)
	var cursor int64
	if err := dst.QueryRow(`SELECT wake_cursor FROM supervisor`).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 3 {
		t.Fatalf("wake cursor = %d, want 3", cursor)
	}
	var seq int64
	if err := dst.QueryRow(`SELECT seq FROM event WHERE detail LIKE '%a1b2c3d%'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := dst.QueryRow(`SELECT detail FROM event WHERE seq = ?`, seq).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "a1b2c3d") {
		t.Fatalf("commit-like ref was rewritten: %q", detail)
	}
	var url string
	if err := dst.QueryRow(`SELECT detail FROM event WHERE kind = 'board.opened'`).Scan(&url); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "/"+p.dstID+"/decision/d2") || strings.Contains(url, "/"+p.srcID+"/") {
		t.Fatalf("cross-fleet URL = %q (source %s, destination %s)", url, p.srcID, p.dstID)
	}

	if b, err := os.ReadFile(filepath.Join(p.dst, "memory", "operator.md")); err != nil || string(b) != "destination rules\n" {
		t.Fatalf("operator.md = %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(p.dst, "memory", "operator."+p.srcID+".md")); err != nil || string(b) != "source rules t1\n" {
		t.Fatalf("saved operator.md = %q, %v", b, err)
	}
	for _, rel := range []string{"memory/projects/solo.md", "memory/projects/srcweb.md", "reports/t1/report.md", "briefs/t1/brief.md", "shots/t1/shot.txt", "deliverables/t1/deliver.txt", "plans/t1/plan.md", "inbox/20261104-000000-1.txt", "projects/solo/README", "projects/web/README"} {
		if _, err := os.Stat(filepath.Join(p.dst, rel)); err != nil {
			t.Fatalf("merged file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.dst, "memory", "projects", "web.md")); !os.IsNotExist(err) {
		t.Fatalf("renamed project memory kept the old name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.src, "hand.db")); err != nil {
		t.Fatalf("source home was removed: %v", err)
	}
	assertCount(t, src, `SELECT COUNT(*) FROM task`, 2)
	assertCount(t, src, `SELECT COUNT(*) FROM event`, 3)
}

func TestFleetMergeRefusesALiveAttemptAndRollsBackABadForeignKey(t *testing.T) {
	p := newMergePair(t)
	db := openMergeDB(t, p.src)
	if _, err := db.Exec(`UPDATE attempt SET status = 'running' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--dry-run", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "live attempt") {
		t.Fatalf("live attempt: code=%d stderr=%q", code, errOut)
	}

	db = openMergeDB(t, p.src)
	if _, err := db.Exec(`UPDATE attempt SET status = 'stopped' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project_grant(project, harness, pattern, created_at) VALUES ('gone', 'claude', 'git status', '2026-11-04T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := dumpMergeState(t, p.dst)
	_, errOut, code = p.h.run("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code == 0 || !strings.Contains(strings.ToLower(errOut), "foreign key") {
		t.Fatalf("bad foreign key: code=%d stderr=%q", code, errOut)
	}
	if after := dumpMergeState(t, p.dst); after != before {
		t.Fatalf("rollback changed the destination:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(p.dst, "reports", "t1", "report.md")); !os.IsNotExist(err) {
		t.Fatalf("rollback moved files: %v", err)
	}
}

func TestFleetMergeArchivesOnlyWhenAsked(t *testing.T) {
	p := newMergePair(t)
	out := p.h.ok("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", "--archive-source", p.srcID, "INTO", p.dstID)
	if !strings.Contains(out, "merged-into written and verified, registry file removed") {
		t.Fatalf("archive field: %q", field(out, "archive"))
	}
	if _, err := os.Stat(filepath.Join(p.src, "merged-into")); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.h.vars["SECONDHAND_HOME"], "fleets", p.srcID)); !os.IsNotExist(err) {
		t.Fatalf("registry link still exists: %v", err)
	}
	if _, err := os.Stat(p.src); err != nil {
		t.Fatalf("source home was removed: %v", err)
	}
}

func newMergePair(t *testing.T) mergePair {
	t.Helper()
	h := newHarness(t)
	p := mergePair{h: h, src: t.TempDir(), dst: t.TempDir()}
	p.srcID = field(h.ok("init", "--name", "personal", p.src), "id")
	p.dstID = field(h.ok("init", "--name", "hand", p.dst), "id")
	h.vars["LUVUS_HOME"] = t.TempDir()
	h.vars["PATH"] = os.Getenv("PATH")
	fillMergeFixture(t, p)
	return p
}

func fillMergeFixture(t *testing.T, p mergePair) {
	t.Helper()
	for _, dir := range []string{"src", "dst"} {
		home := p.src
		if dir == "dst" {
			home = p.dst
		}
		db := openMergeDB(t, home)
		for _, q := range []string{
			`DELETE FROM event`, `DELETE FROM supervisor_input`, `DELETE FROM timer`, `DELETE FROM report`, `DELETE FROM decision`, `DELETE FROM plan`, `DELETE FROM attempt`, `DELETE FROM task`, `DELETE FROM project_grant`, `DELETE FROM project`, `DELETE FROM supervisor`, `DELETE FROM device`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
	}

	src := openMergeDB(t, p.src)
	dst := openMergeDB(t, p.dst)
	defer src.Close()
	defer dst.Close()
	mustExec(t, src, `INSERT INTO project(name, repo, settings, created_at) VALUES ('web', ?, '', '2026-11-01T00:00:00Z'), ('solo', ?, '', '2026-11-01T00:00:00Z')`, p.src+"/projects/web", p.src+"/projects/solo")
	mustExec(t, src, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES
		(1, 'web', 'Fix t1 with a1 and r1, see d1 and w1 and i1', ?, 'active', '2026-11-01T00:00:00Z', '2026-11-04T00:00:00Z'),
		(2, 'solo', 'Second t2', 'see t1', 'done', '2026-11-01T00:00:00Z', '2026-11-04T00:00:00Z')`,
		"home "+p.src+" branch hand/"+p.srcID+"/t1-a1 path reports/t1 briefs/t1 shots/t1")
	mustExec(t, src, `INSERT INTO plan(task_id, revision, body, created_at) VALUES (1, 1, 'Plan t1 a1 r1 d1 w1 i1 `+p.src+`/x', '2026-11-02T00:00:00Z')`)
	mustExec(t, src, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, 'Q a1 d1 i1', 'answered', 'A r1 w1', 'operator', '2026-11-02T00:00:00Z', '2026-11-02T01:00:00Z')`)
	mustExec(t, src, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, reason, created_at, ended_at, continues, approve, access, kept) VALUES
		(1, 1, 'claude', 'sonnet', 'low', ?, ?, ?, 'stopped', '', '2026-11-02T00:00:00Z', '2026-11-03T00:00:00Z', 0, 'never', 'open', ''),
		(2, 2, 'claude', 'sonnet', 'low', ?, ?, ?, 'interrupted', 'reaped', '2026-11-03T00:00:00Z', '2026-11-03T01:00:00Z', 1, 'ask', 'readonly', 'kept')`,
		`["claude","--prompt","see t1 a1 r1 d1 w1 i1 hand/`+p.srcID+`/t1-a1 `+p.src+`/x"]`,
		"/wt/"+p.srcID+"/t1-a1", "hand/"+p.srcID+"/t1-a1",
		`["claude","--prompt","continue t2"]`, "/wt/"+p.srcID+"/t2-a2", "hand/"+p.srcID+"/t2-a2")
	mustExec(t, src, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at, acked_at, acked_by) VALUES (1, 1, 1, 'done', 'Done t1 a1 r1 d1 w1 i1 `+p.src+`/reports/t1', '2026-11-03T01:00:00Z', '', '')`)
	mustExec(t, src, `INSERT INTO timer(id, due_at, every, task_id, note, status, created_at, ended_at, waits) VALUES (1, '2027-01-01T00:00:00Z', 0, 1, 'note t1 a1 r1 d1 w1 i1', 'armed', '2026-11-03T00:00:00Z', '', 0)`)
	mustExec(t, src, `INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, 'input t1 a1 r1 d1 w1 i1 `+p.src+`/x', '2026-11-03T00:00:00Z', '2026-11-03T00:01:00Z')`)
	mustExec(t, src, `INSERT INTO supervisor(id, harness, model, effort, argv, status, wake_cursor, created_at, ended_at) VALUES (1, 'claude', 'sonnet', 'low', '["claude"]', 'stopped', 0, '2026-11-01T00:00:00Z', '2026-11-02T00:00:00Z')`)
	mustExec(t, src, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES
		(10, '2026-11-04T00:00:00Z', 'task.added', 1, 't1 a1 r1 d1 w1 i1 `+p.src+`/x'),
		(11, '2026-11-04T02:00:00Z', 'task.added', 2, 't2 a2'),
		(12, '2026-11-04T03:00:00Z', 'task.added', 1, 'commit a1b2c3d remains')`)

	mustExec(t, dst, `INSERT INTO project(name, repo, settings, created_at) VALUES ('web', ?, '', '2026-10-01T00:00:00Z'), ('app', ?, '', '2026-10-01T00:00:00Z')`, p.dst+"/vendor/web", p.dst+"/projects/app")
	mustExec(t, dst, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES
		(1, 'web', 'Destination one t1 a1', 'keep t1', 'done', '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z'),
		(2, 'app', 'Destination two', 'see t1 and a1', 'active', '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z'),
		(3, 'app', 'Destination three', 'nothing', 'inbox', '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z')`)
	mustExec(t, dst, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at, ended_at, approve, access) VALUES (1, 1, 'claude', 'sonnet', 'low', '["claude"]', '/wt/dst/t1-a1', 'hand/`+p.dstID+`/t1-a1', 'stopped', '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z', 'never', 'open')`)
	mustExec(t, dst, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (1, 1, 1, 'done', 'destination report r1', '2026-10-02T00:00:00Z')`)
	mustExec(t, dst, `INSERT INTO decision(id, task_id, question, status, answer, answered_by, created_at, closed_at) VALUES (1, 1, 'destination d1', 'answered', 'yes', 'operator', '2026-10-02T00:00:00Z', '2026-10-02T01:00:00Z')`)
	mustExec(t, dst, `INSERT INTO timer(id, due_at, every, task_id, note, status, created_at, ended_at, waits) VALUES (1, '2026-10-03T00:00:00Z', 0, 1, 'destination w1', 'fired', '2026-10-02T00:00:00Z', '2026-10-03T00:00:00Z', 0)`)
	mustExec(t, dst, `INSERT INTO supervisor_input(id, body, created_at, delivered_at) VALUES (1, 'destination i1', '2026-10-02T00:00:00Z', '2026-10-02T00:01:00Z')`)
	mustExec(t, dst, `INSERT INTO supervisor(id, harness, model, effort, argv, status, wake_cursor, created_at, ended_at) VALUES (1, 'claude', 'sonnet', 'low', '["claude"]', 'stopped', 2, '2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z')`)
	mustExec(t, dst, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES
		(1, '2026-11-03T00:00:00Z', 'board.opened', 0, 'http://127.0.0.1:8787/`+p.srcID+`/decision/d1'),
		(2, '2026-11-04T01:00:00Z', 'task.added', 2, 't1 a1'),
		(3, '2026-11-06T00:00:00Z', 'task.done', 3, 't2')`)

	files := map[string]string{
		"memory/operator.md":          "source rules t1\n",
		"memory/projects/solo.md":     "solo facts\n",
		"memory/projects/web.md":      "web facts t1\n",
		"reports/t1/report.md":        "report t1\n",
		"briefs/t1/brief.md":          "brief t1\n",
		"shots/t1/shot.txt":           "shot t1\n",
		"deliverables/t1/deliver.txt": "deliverable t1\n",
		"plans/t1/plan.md":            "plan t1\n",
		"inbox/20261104-000000-1.txt": "inbox\n",
		"projects/solo/README":        "solo repo\n",
		"projects/web/README":         "web repo\n",
	}
	for rel, body := range files {
		writeMergeFile(t, filepath.Join(p.src, rel), body)
	}
	writeMergeFile(t, filepath.Join(p.dst, "memory/operator.md"), "destination rules\n")
	writeMergeFile(t, filepath.Join(p.dst, "memory/projects/app.md"), "app facts\n")
}

func writeMergeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openMergeDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "hand.db")+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func assertCount(t *testing.T, q interface {
	QueryRow(string, ...any) *sql.Row
}, query string, want int64) {
	t.Helper()
	var got int64
	if err := q.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func dumpMergeState(t *testing.T, home string) string {
	t.Helper()
	db := openMergeDB(t, home)
	defer db.Close()
	var b strings.Builder
	for _, q := range []string{
		`SELECT 'task|'||id||'|'||title||'|'||goal FROM task ORDER BY id`,
		`SELECT 'project|'||name||'|'||repo FROM project ORDER BY name`,
		`SELECT 'event|'||seq||'|'||detail FROM event ORDER BY seq`,
		`SELECT 'report|'||id||'|'||body FROM report ORDER BY id`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		rows.Close()
	}
	return b.String()
}

func TestFleetMergeOnCopies(t *testing.T) {
	root := os.Getenv("HAND_MERGE_COPIES")
	if root == "" {
		t.Skip("set HAND_MERGE_COPIES to a folder holding hand/hand.db and personal/hand.db")
	}
	h := newHarness(t)
	h.vars["PATH"] = os.Getenv("PATH")
	h.vars["LUVUS_HOME"] = t.TempDir()
	p := mergePair{h: h, src: filepath.Join(h.home, "personal"), dst: filepath.Join(h.home, "hand")}
	for _, dir := range []string{p.src, p.dst} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, home := range map[string]string{"personal": p.src, "hand": p.dst} {
		if err := state.CopyDatabase(filepath.Join(root, name, "hand.db"), filepath.Join(home, "hand.db")); err != nil {
			t.Fatal(err)
		}
	}
	sh, err := state.InspectFleet(filepath.Join(p.src, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	dh, err := state.InspectFleet(filepath.Join(p.dst, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	p.srcID, p.dstID = sh.Fleet.ID, dh.Fleet.ID
	rootDir := h.vars["SECONDHAND_HOME"]
	for id, home := range map[string]string{p.srcID: p.src, p.dstID: p.dst} {
		writeMergeFile(t, filepath.Join(rootDir, "fleets", id), home+"\n")
	}
	db := openMergeDB(t, p.dst)
	if _, err := db.Exec(`UPDATE supervisor SET status = 'stopped' WHERE status IN ('launching', 'running')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE attempt SET status = 'interrupted' WHERE status IN ('launching', 'running')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	args := []string{"--home", p.dst, "fleet", "merge", "--dry-run", "--cancel-timers", p.srcID, "INTO", p.dstID}
	first := h.ok(args...)
	second := h.ok(args...)
	if first != second {
		t.Fatalf("two dry runs of the copies differ:\n--- first\n%s\n--- second\n%s", first, second)
	}
	srcCount := countMergeRows(t, p.src, "task")
	dstCount := countMergeRows(t, p.dst, "task")
	if !strings.Contains(first, fmt.Sprintf("task,%d,%d,%d,", srcCount, dstCount, srcCount+dstCount)) {
		t.Fatalf("task counts missing from:\n%s", first)
	}
}

func countMergeRows(t *testing.T, home, table string) int64 {
	t.Helper()
	db := openMergeDB(t, home)
	defer db.Close()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFleetMergeRefusesAnOldSchema(t *testing.T) {
	p := newMergePair(t)
	db := openMergeDB(t, p.src)
	if _, err := db.Exec(`PRAGMA user_version = 17`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--dry-run", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 2 || !strings.Contains(errOut, "has schema 17") {
		t.Fatalf("old schema: code=%d stderr=%q", code, errOut)
	}
}

func TestFleetMergeRefusesDryRunStopAndPreflightsBeforeStopping(t *testing.T) {
	p := newMergePair(t)
	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--dry-run", "--stop", p.srcID, "INTO", p.dstID)
	if code != 2 || !strings.Contains(errOut, "--dry-run") {
		t.Fatalf("dry-run stop: code=%d stderr=%q", code, errOut)
	}
	startMergeWatcher(t, p.src)
	_, errOut, code = p.h.run("--home", p.dst, "fleet", "merge", "--stop", "--rename-project", "web=srcweb", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "armed timer") {
		t.Fatalf("armed timer: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(p.src, "watch.pid")); err != nil {
		t.Fatalf("a refused merge stopped the source watcher: %v", err)
	}
	_, errOut, code = p.h.run("--home", p.dst, "fleet", "merge", "--stop", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "project web exists on both sides") {
		t.Fatalf("project collision: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(p.src, "watch.pid")); err != nil {
		t.Fatalf("a project refusal stopped the source watcher: %v", err)
	}
	writeMergeFile(t, filepath.Join(p.dst, "reports", "t1", "report.md"), "destination report\n")
	_, errOut, code = p.h.run("--home", p.dst, "fleet", "merge", "--stop", "--cancel-timers", "--rename-project", "web=srcweb", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "different content") {
		t.Fatalf("file collision: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(p.src, "watch.pid")); err != nil {
		t.Fatalf("a file refusal stopped the source watcher: %v", err)
	}
}

func startMergeWatcher(t *testing.T, home string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "watch")
	cmd.Env = append(os.Environ(), "HAND_HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(home, "watch.started"))
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitMerge(t, func() bool {
		_, err := os.Stat(filepath.Join(home, "watch.started"))
		return err == nil
	}, "the watcher to start")
	waitMerge(t, func() bool {
		f, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return false
		}
		defer f.Close()
		ok, err := flock.Lock(f, false)
		if err != nil {
			return false
		}
		if !ok {
			return true
		}
		_ = flock.Release(f)
		return false
	}, "the watcher lock")
	marker, err := luvus.ProcStartMarker(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	writePID(t, filepath.Join(home, "watch.pid"), cmd.Process.Pid, marker, selfExe(t))
}

func waitMerge(t *testing.T, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFleetMergeCancelTimersWithLowSourceSequences(t *testing.T) {
	p := newMergePair(t)
	db := openMergeDB(t, p.src)
	mustExec(t, db, `DELETE FROM event`)
	mustExec(t, db, `INSERT INTO event(seq, at, kind, task_id, detail) VALUES
		(1, '2026-11-04T00:00:00Z', 'task.added', 1, 't1'),
		(2, '2026-11-04T01:00:00Z', 'task.added', 2, 't2'),
		(3, '2026-11-04T02:00:00Z', 'task.added', 1, 't1 again')`)
	db.Close()
	out := p.h.ok("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if !strings.Contains(out, "event,3,3,8,3") {
		t.Fatalf("event counts missing from:\n%s", out)
	}
	db = openMergeDB(t, p.dst)
	defer db.Close()
	assertCount(t, db, `SELECT COUNT(*) FROM event WHERE kind = 'timer.cancelled'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM event`, 8)
}

func TestFleetMergeRefusesASecondRealRun(t *testing.T) {
	p := newMergePair(t)
	p.h.ok("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	before := dumpMergeState(t, p.dst)
	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb2", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "already merged") || !strings.Contains(errOut, "non-operation") {
		t.Fatalf("second run: code=%d stderr=%q", code, errOut)
	}
	if after := dumpMergeState(t, p.dst); after != before {
		t.Fatalf("a second run changed the destination:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

func TestFleetMergeRefusesDuplicateFileDestinations(t *testing.T) {
	p := newMergePair(t)
	writeMergeFile(t, filepath.Join(p.src, "memory", "projects", "srcweb.md"), "other web facts\n")
	before := dumpMergeState(t, p.dst)
	_, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	if code != 3 || !strings.Contains(errOut, "both map to") {
		t.Fatalf("duplicate targets: code=%d stderr=%q", code, errOut)
	}
	if after := dumpMergeState(t, p.dst); after != before {
		t.Fatalf("a duplicate file target changed the destination:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(p.dst, "reports", "t1", "report.md")); !os.IsNotExist(err) {
		t.Fatalf("a duplicate file target moved files: %v", err)
	}
}

func TestFleetMergeDedupesProjectGrants(t *testing.T) {
	p := newMergePair(t)
	src := openMergeDB(t, p.src)
	mustExec(t, src, `INSERT INTO project(name, repo, settings, created_at) VALUES ('app', ?, '', '2026-11-01T00:00:00Z')`, p.src+"/projects/app")
	mustExec(t, src, `INSERT INTO project_grant(project, harness, pattern, created_at) VALUES ('app', 'claude', 'git status', '2026-10-01T00:00:00Z')`)
	src.Close()
	dst := openMergeDB(t, p.dst)
	mustExec(t, dst, `INSERT INTO project_grant(project, harness, pattern, created_at) VALUES ('app', 'claude', 'git status', '2026-11-01T00:00:00Z')`)
	dst.Close()
	p.h.ok("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", p.srcID, "INTO", p.dstID)
	dst = openMergeDB(t, p.dst)
	defer dst.Close()
	assertCount(t, dst, `SELECT COUNT(*) FROM project_grant`, 1)
	var created string
	if err := dst.QueryRow(`SELECT created_at FROM project_grant`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != "2026-10-01T00:00:00Z" {
		t.Fatalf("grant created_at = %q, want the earliest", created)
	}
}

func TestFleetMergeReportsAPostCommitArchiveFailure(t *testing.T) {
	p := newMergePair(t)
	if err := os.MkdirAll(filepath.Join(p.src, "merged-into"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := p.h.run("--home", p.dst, "fleet", "merge", "--rename-project", "web=srcweb", "--cancel-timers", "--archive-source", p.srcID, "INTO", p.dstID)
	if code == 0 {
		t.Fatalf("archive with a blocked marker succeeded:\n%s", out)
	}
	if !strings.Contains(out, "counts[13]") || !strings.Contains(out, "post_commit_error") {
		t.Fatalf("no post-commit manifest:\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(out, "moves[") || !strings.Contains(out, "reports/t1/report.md") {
		t.Fatalf("manifest does not list completed file moves:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(p.h.vars["SECONDHAND_HOME"], "fleets", p.srcID)); err != nil {
		t.Fatalf("archive removed the registry before the marker: %v", err)
	}
	dst := openMergeDB(t, p.dst)
	defer dst.Close()
	assertCount(t, dst, `SELECT COUNT(*) FROM task`, 5)
}
