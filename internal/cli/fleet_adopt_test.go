package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
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

func lockWatch(t *testing.T, home string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := flock.Lock(f, false); err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	return func() { _ = flock.Release(f) }
}

func TestFleetAdoptRefusesAWatcherAndABoard(t *testing.T) {
	for _, c := range []struct {
		name string
		hold func(*testing.T, *harness, string) func()
		want string
	}{
		{"watcher", func(t *testing.T, _ *harness, home string) func() { return lockWatch(t, home) }, "the watcher is running"},
		{"board", func(t *testing.T, h *harness, _ string) func() {
			forget, err := update.WritePID(filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid"))
			if err != nil {
				t.Fatal(err)
			}
			return forget
		}, "the board is running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, old, copied := adoptCopy(t)
			if err := os.RemoveAll(old); err != nil {
				t.Fatal(err)
			}
			forget := c.hold(t, h, copied)
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

func TestFleetAdoptIgnoresABoardThatDoesNotServeTheFleet(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.vars["SECONDHAND_HOME"], "index", fleetIDOf(t, copied))); err != nil {
		t.Fatal(err)
	}
	forget, err := update.WritePID(filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid"))
	if err != nil {
		t.Fatal(err)
	}
	defer forget()
	h.ok("orient")
	if got := recordedHome(t, copied); got != copied {
		t.Fatalf("recorded home = %q, want the adoption beside a board that does not know the fleet", got)
	}
}

func fleetIDOf(t *testing.T, home string) string {
	t.Helper()
	var id string
	if err := openMoveDB(t, home).QueryRow(`SELECT id FROM fleet`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFleetAdoptRefusesAWatcherThatHoldsTheLockWithoutAPIDFile(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(copied, "watch.pid")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	release := lockWatch(t, copied)
	defer release()
	if _, errOut, code := h.run("orient"); code != 3 || !strings.Contains(errOut, "the watcher is running") {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
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

func renameFleetHome(t *testing.T, h *harness, name string) (old, dest string) {
	t.Helper()
	old = h.home
	dest = moveCanonicalDest(t, filepath.Join(filepath.Dir(old), name))
	if err := os.Rename(old, dest); err != nil {
		t.Fatal(err)
	}
	h.home, h.cwd = dest, dest
	return old, dest
}

func TestSupervisorStopWorksWhereAnAdoptionRefuses(t *testing.T) {
	h, _ := newMovedSupervisorFixture(t)
	startClaudeSupervisor(h)
	old, dest := renameFleetHome(t, h, "fleet-personal")
	for _, args := range [][]string{{"orient"}, {"fleet", "adopt"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "supervisor s1 is running") || !strings.Contains(errOut, "hand supervisor stop") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := recordedHome(t, dest); got != old {
		t.Fatalf("a refused adoption changed the database: %q", got)
	}
	has(t, "stop", h.ok("supervisor", "stop"), "supervisor: s1", "status: stopped")
	if got := recordedHome(t, dest); got != old {
		t.Fatalf("stop adopted the fleet: %q", got)
	}
	h.ok("orient")
	if got := recordedHome(t, dest); got != dest {
		t.Fatalf("recorded home = %q after the supervisor stopped", got)
	}
}

func TestSupervisorInterruptWorksWhereAnAdoptionRefuses(t *testing.T) {
	h, _ := newMovedSupervisorFixture(t)
	startClaudeSupervisor(h)
	old, dest := renameFleetHome(t, h, "fleet-personal")
	if _, errOut, code := h.run("orient"); code != 3 {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("supervisor", "interrupt"); code != 0 || strings.Contains(errOut, "moved here") {
		t.Fatalf("interrupt: code=%d stderr=%q", code, errOut)
	}
	if got := recordedHome(t, dest); got != old {
		t.Fatalf("interrupt adopted the fleet: %q", got)
	}
}

func TestSupervisorResumeStartsFreshAfterAnAdoption(t *testing.T) {
	h, rt := newMovedSupervisorFixture(t)
	startClaudeSupervisor(h)
	first := rt.lastCreate().Command[3]
	h.ok("supervisor", "stop")
	h.advance(time.Minute)
	old, dest := renameFleetHome(t, h, "fleet-personal")
	out := h.ok("supervisor", "resume")
	argv := rt.lastCreate().Command
	if slices.Contains(argv, "--resume") || slices.Contains(argv, first) {
		t.Fatalf("argv = %q, want a fresh session, not %s", argv, first)
	}
	if why := field(out, "fresh"); !strings.Contains(why, "moved from "+old+" to "+dest) {
		t.Fatalf("fresh = %q", why)
	}
}

func TestAFailedAdoptLeavesNoCopyAndNamesTheCell(t *testing.T) {
	h, old, _ := initMoveFleet(t)
	seedMoveFleet(t, old, fleetIDOf(t, old))
	dest := moveCanonicalDest(t, filepath.Join(t.TempDir(), strings.Repeat("d", 60)))
	if err := os.Rename(old, dest); err != nil {
		t.Fatal(err)
	}
	h.home, h.cwd = dest, dest
	title := strings.Repeat("x", 200-len(old)-1) + " " + old
	moveExec(t, openMoveDB(t, dest), `UPDATE task SET title = ? WHERE id = 1`, title)
	for range 3 {
		if _, errOut, code := h.run("orient"); code != 3 || !strings.Contains(errOut, "task.title") || !strings.Contains(errOut, "edit that cell by hand") {
			t.Fatalf("orient: code=%d stderr=%q", code, errOut)
		}
	}
	if _, errOut, code := h.run("fleet", "adopt"); code != 3 || !strings.Contains(errOut, "task.title") {
		t.Fatalf("adopt: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("fleet", "adopt", "--check"); code != 3 || !strings.Contains(errOut, "task.title") {
		t.Fatalf("--check: code=%d stderr=%q", code, errOut)
	}
	if got := recordedHome(t, dest); got != old {
		t.Fatalf("a failed adoption changed the database: %q", got)
	}
	if names := preAdoptCopies(t, dest); len(names) != 0 {
		t.Fatalf("a failed adoption left %v", names)
	}
}

func preAdoptCopies(t *testing.T, home string) []string {
	t.Helper()
	des, err := os.ReadDir(fleet.Backups(home))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		if strings.HasPrefix(de.Name(), "pre-adopt-") {
			names = append(names, de.Name())
		}
	}
	return names
}

func TestFleetAdoptRefusesWhileAMoveIsPending(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(filepath.Dir(copied), "elsewhere")
	moveExec(t, openMoveDB(t, copied), `INSERT INTO event(at, kind, task_id, detail) VALUES (?, 'fleet.moving', NULL, ?)`, moveAt, moveEventDetail(t, old, dest))
	for _, args := range [][]string{{"fleet", "adopt"}, {"fleet", "adopt", "--check"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "hand fleet move --yes "+dest) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("a refused adoption changed the database: %q", got)
	}
}

func TestFleetAdoptIgnoresAnotherFleetAtAnOldPath(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	other := *h
	other.home, other.cwd = old, old
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	other.ok("init")
	if fleetIDOf(t, old) == fleetIDOf(t, copied) {
		t.Fatal("the second fleet has the first one's id")
	}
	h.ok("orient")
	if got := recordedHome(t, copied); got != copied {
		t.Fatalf("recorded home = %q beside another fleet at the old path", got)
	}
}

func TestFleetAdoptNamesAnUnreadableOldDatabase(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "hand.db"), []byte("not a database at all, just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := h.run("orient")
	if code != 3 || !strings.Contains(errOut, "cannot read "+filepath.Join(old, "hand.db")) || strings.Contains(errOut, "conflict: conflict:") {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("a refused adoption changed the database: %q", got)
	}
}

func TestFleetAdoptFromTheCurrentHomeIsCurrent(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	for _, args := range [][]string{{"fleet", "adopt", "--from", h.home}, {"fleet", "adopt", "--check", "--from", h.home}} {
		if out := h.ok(args...); !strings.Contains(out, "adopt: current") {
			t.Fatalf("%q = %q", args, out)
		}
	}
	if names := preAdoptCopies(t, h.home); len(names) != 0 {
		t.Fatalf("pre-adopt copies = %v", names)
	}
}

func TestFleetAdoptRefusesANewHomeInsideAnOldOne(t *testing.T) {
	h, old, _ := initMoveFleet(t)
	seedMoveFleet(t, old, fleetIDOf(t, old))
	inner := filepath.Join(old, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hand.db", "fleet.home"} {
		b, err := os.ReadFile(filepath.Join(old, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inner, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"hand.db", "hand.db-wal", "hand.db-shm"} {
		_ = os.Remove(filepath.Join(old, name))
	}
	h.home, h.cwd = inner, inner
	for _, args := range [][]string{{"orient"}, {"fleet", "adopt"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "is inside the old home "+old) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if out := h.ok("fleet", "adopt", "--check"); !strings.Contains(out, "blocked[1]:") || !strings.Contains(out, "is inside the old home") {
		t.Fatalf("--check = %q", out)
	}
	if names := preAdoptCopies(t, inner); len(names) != 0 {
		t.Fatalf("a refused adoption left %v", names)
	}
}

func TestFleetAdoptRemovesItsCopyWhenNothingChanged(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	gone := filepath.Join(filepath.Dir(h.home), "gone", "fleet")
	if err := os.WriteFile(filepath.Join(h.home, fleet.HomeFile), []byte(`{"homes":["`+moveFieldPath(gone)+`"],"current":"`+moveFieldPath(gone)+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("orient"); code != 0 || strings.Contains(errOut, "adopted") {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
	if names := preAdoptCopies(t, h.home); len(names) != 0 {
		t.Fatalf("an adoption that changed nothing left %v", names)
	}
	if n := adoptCount(t, h.home, `SELECT count(*) FROM event WHERE kind = 'fleet.adopted'`); n != 0 {
		t.Fatalf("%d fleet.adopted events for a no-op", n)
	}
}

func TestInitFirstInAMovedHomeReportsTheMoveAndTheWatchUnit(t *testing.T) {
	h, old, _ := initMoveFleet(t)
	dest := moveCanonicalDest(t, filepath.Join(filepath.Dir(old), "fleet-personal"))
	if err := os.Rename(old, dest); err != nil {
		t.Fatal(err)
	}
	h.home, h.cwd = dest, dest
	if out := h.ok("init"); field(out, "moved_from") != old || !strings.Contains(out, "remove any watch unit") {
		t.Fatalf("init after move = %q", out)
	}
	if _, errOut, code := h.run("task", "list"); code != 0 || !strings.Contains(errOut, "hand: adopted "+dest) {
		t.Fatalf("first command after init: code=%d stderr=%q", code, errOut)
	}
}
