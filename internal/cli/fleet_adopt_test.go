package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/update"
)

func adoptCopy(t *testing.T) (h *harness, old, copied string) {
	t.Helper()
	h, old, id := initMoveFleet(t)
	seedMoveFleet(t, old, id)
	copied = moveCanonicalDest(t, filepath.Join(t.TempDir(), "copy"))
	copyMoveTree(t, old, copied)
	h.cwd, h.home = copied, copied
	return h, old, copied
}

func adoptCount(t *testing.T, home, query string, args ...any) int {
	t.Helper()
	var n int
	if err := openMoveDB(t, home).QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func recordedHome(t *testing.T, home string) string {
	t.Helper()
	var recorded string
	if err := openMoveDB(t, home).QueryRow(`SELECT home FROM fleet`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	return recorded
}

func TestFleetAdoptRewritesACopyOnItsFirstCommand(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("the copy records %q, want %q", got, old)
	}
	check := h.ok("fleet", "adopt", "--check")
	for _, want := range []string{"adopt: check", "from[1]:", "verification: 0", "report,body,1,"} {
		if !strings.Contains(check, want) {
			t.Fatalf("--check missing %q:\n%s", want, check)
		}
	}
	h.run("doctor")
	h.ok("fleet", "list")
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("--check, doctor or fleet list rewrote the database: %q", got)
	}
	if n := adoptCount(t, copied, `SELECT count(*) FROM event WHERE kind = 'fleet.adopted'`); n != 0 {
		t.Fatalf("%d fleet.adopted events before any adoption", n)
	}
	_, errOut, code := h.run("orient")
	if code != 0 || !strings.Contains(errOut, "hand: adopted "+copied) {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
	if got := recordedHome(t, copied); got != copied {
		t.Fatalf("recorded home = %q, want %q", got, copied)
	}
	for _, table := range []struct{ name, column string }{{"report", "body"}, {"attempt", "argv"}, {"supervisor", "argv"}, {"decision", "question"}, {"event", "detail"}} {
		q := `SELECT count(*) FROM ` + table.name + ` WHERE instr(` + table.column + `, ?) > 0`
		if table.name == "event" {
			q += ` AND kind <> 'fleet.adopted'`
		}
		if n := adoptCount(t, copied, q, old); n != 0 {
			t.Fatalf("%s.%s still names the old home in %d cells", table.name, table.column, n)
		}
	}
	if out := h.ok("task", "show", "t1"); !strings.Contains(out, moveFieldPath(copied)+"/a") {
		t.Fatalf("task show = %q", out)
	}
	if out := h.ok("report", "show", "r1"); !strings.Contains(field(out, "body"), "Delivered: "+copied+"/reports/t1/x.md") {
		t.Fatalf("report show = %q", out)
	}
	if out := h.ok("open", "--print", "--delivered", "t1"); !strings.HasPrefix(field(out, "path"), copied) {
		t.Fatalf("open --delivered = %q", out)
	}
	if got := adoptCount(t, copied, `SELECT count(*) FROM event WHERE kind = 'fleet.adopted' AND detail LIKE ?`, "%"+moveFieldPath(old)+"%"); got != 1 {
		t.Fatalf("fleet.adopted events naming the old home = %d, want 1", got)
	}
	if got := adoptCount(t, copied, `SELECT count(*) FROM pragma_foreign_key_check`); got != 0 {
		t.Fatalf("foreign_key_check found %d rows", got)
	}
	var integrity string
	if err := openMoveDB(t, copied).QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
	rec, err := fleet.ReadHomes(copied)
	if err != nil || rec.Current != copied {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if list := h.ok("fleet", "list"); !strings.Contains(list, moveFieldPath(copied)) || !strings.Contains(list, ",ok") {
		t.Fatalf("fleet list = %q", list)
	}
	events := adoptCount(t, copied, `SELECT count(*) FROM event`)
	for _, args := range [][]string{{"fleet", "adopt"}, {"fleet", "adopt", "--from", old}, {"orient"}} {
		out := h.ok(args...)
		if len(args) == 2 && !strings.Contains(out, "adopt: current") {
			t.Fatalf("second %q = %q", args, out)
		}
	}
	if got := adoptCount(t, copied, `SELECT count(*) FROM event`); got != events {
		t.Fatalf("a second run wrote %d events", got-events)
	}
	if n := adoptCount(t, copied, `SELECT count(*) FROM event WHERE kind = 'fleet.adopted'`); n != 1 {
		t.Fatalf("fleet.adopted events after repeats = %d, want 1", n)
	}
}

func TestFleetAdoptRefusesWhileAnOldHomeStillExists(t *testing.T) {
	h, old, copied := adoptCopy(t)
	for _, args := range [][]string{{"orient"}, {"fleet", "adopt"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "also at "+old) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("a refused adoption changed the database: %q", got)
	}
	if out := h.ok("fleet", "adopt", "--check"); !strings.Contains(out, "blocked[1]:") || !strings.Contains(out, "also at "+old) {
		t.Fatalf("--check = %q", out)
	}
}

func TestFleetAdoptRefusesAWatcherAndABoard(t *testing.T) {
	for _, c := range []struct {
		name string
		path func(*harness, string) string
		want string
	}{
		{"watcher", func(_ *harness, home string) string { return filepath.Join(home, "watch.pid") }, "the watcher is running"},
		{"board", func(h *harness, _ string) string { return filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid") }, "the board is running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, old, copied := adoptCopy(t)
			if err := os.RemoveAll(old); err != nil {
				t.Fatal(err)
			}
			forget, err := update.WritePID(c.path(h, copied))
			if err != nil {
				t.Fatal(err)
			}
			if _, errOut, code := h.run("orient"); code != 3 || !strings.Contains(errOut, c.want) {
				t.Fatalf("orient: code=%d stderr=%q", code, errOut)
			}
			if got := recordedHome(t, copied); got != old {
				t.Fatalf("a refused adoption changed the database: %q", got)
			}
			forget()
			h.ok("orient")
			if got := recordedHome(t, copied); got != copied {
				t.Fatalf("recorded home = %q after the service stopped", got)
			}
		})
	}
}

func TestFleetAdoptRewritesEveryKnownAndGivenHome(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	h.ok("orient")
	earlier := filepath.Join(filepath.Dir(copied), "earlier", "fleet-hand")
	db := openMoveDB(t, copied)
	moveExec(t, db, `UPDATE report SET body = body || ?`, " and "+earlier+"/reports/x.md")
	if out := h.ok("fleet", "adopt", "--check", "--from", earlier); !strings.Contains(out, "report,body,1,1") {
		t.Fatalf("--check --from = %q", out)
	}
	out := h.ok("fleet", "adopt", "--from", earlier)
	if !strings.Contains(out, "adopt: adopted") || !strings.Contains(out, "report,body,1,1") {
		t.Fatalf("adopt --from = %q", out)
	}
	if n := adoptCount(t, copied, `SELECT count(*) FROM report WHERE instr(body, ?) > 0`, earlier); n != 0 {
		t.Fatalf("%d reports still name %s", n, earlier)
	}
	if n := adoptCount(t, copied, `SELECT count(*) FROM report WHERE instr(body, ?) > 0`, copied+"/reports/x.md"); n != 1 {
		t.Fatalf("reports naming the rewritten path = %d, want 1", n)
	}
}
