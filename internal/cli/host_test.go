package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/boot"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/update"
)

func asHost(t *testing.T, name, boot string) {
	t.Helper()
	old := *cli.HostIdentity
	*cli.HostIdentity = func() (string, string) { return name, boot }
	t.Cleanup(func() { *cli.HostIdentity = old })
}

func putMarker(t *testing.T, h *harness, host, boot string, age time.Duration) {
	t.Helper()
	m := fleet.HostMarker{Host: host, Boot: boot, PID: 31337, At: h.clock().Add(-age)}
	if err := fleet.WriteHost(h.home, m); err != nil {
		t.Fatal(err)
	}
}

func markerBytes(t *testing.T, h *harness) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.home, fleet.HostFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return b
}

func eventCount(t *testing.T, home string, kind string) int {
	t.Helper()
	if kind == "" {
		return adoptCount(t, home, `SELECT count(*) FROM event`)
	}
	return adoptCount(t, home, `SELECT count(*) FROM event WHERE kind = ?`, kind)
}

func seedLiveAttempts(t *testing.T, home string) (dead, alive, launching int64) {
	t.Helper()
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	db := openMoveDB(t, home)
	moveExec(t, db, `INSERT INTO project(name, repo, created_at, settings) VALUES ('app', ?, ?, '{}')`, home+"/repo", moveAt)
	for i := 1; i <= 3; i++ {
		moveExec(t, db, `INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (?, 'app', 'T', 'g', 'active', ?, ?)`, i, moveAt, moveAt)
	}
	ins := func(id, task int64, status string, pid int, mk string) {
		moveExec(t, db, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, pid, start_marker, created_at) VALUES (?, ?, 'claude', 'sonnet', 'low', '[]', 'w', 'b', ?, ?, ?, ?)`, id, task, status, pid, mk, moveAt)
	}
	ins(1, 1, "running", 2000000000, "123")
	ins(2, 2, "running", os.Getpid(), marker)
	ins(3, 3, "launching", 0, "")
	return 1, 2, 3
}

func attemptStatus(t *testing.T, home string, id int64) string {
	t.Helper()
	var s string
	if err := openMoveDB(t, home).QueryRow(`SELECT status FROM attempt WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostMarkerNeverRefusesTheNormalFleet(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	events := eventCount(t, home, "")
	if _, errOut, code := h.run("orient"); code != 0 {
		t.Fatalf("no marker: code=%d stderr=%q", code, errOut)
	}
	if markerBytes(t, h) != nil {
		t.Fatal("a plain command wrote fleet.host")
	}
	for _, c := range []struct {
		name, host string
		age        time.Duration
	}{
		{"fresh", "desk", time.Second},
		{"missed beats for an hour (a busy watcher)", "desk", time.Hour},
		{"suspended for a night", "desk", 9 * time.Hour},
		{"the clock jumped back", "desk", -3 * time.Hour},
		{"the clock jumped forward a month", "desk", 30 * 24 * time.Hour},
		{"the host name changed", "renamed.local", 2 * time.Hour},
	} {
		putMarker(t, h, c.host, "boot-b", c.age)
		before := markerBytes(t, h)
		if _, errOut, code := h.run("orient"); code != 0 || strings.Contains(errOut, "took over") {
			t.Fatalf("%s: code=%d stderr=%q", c.name, code, errOut)
		}
		if !bytes.Equal(markerBytes(t, h), before) {
			t.Fatalf("%s: a plain command rewrote fleet.host", c.name)
		}
	}
	if got := eventCount(t, home, ""); got != events {
		t.Fatalf("the normal fleet gained %d events", got-events)
	}
}

func TestHostMarkerFromAnotherHostRefusesWhileFresh(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	dead, _, _ := seedLiveAttempts(t, home)
	putMarker(t, h, "laptop", "boot-x", 30*time.Second)
	before, events := markerBytes(t, h), eventCount(t, home, "")
	for _, args := range [][]string{{"orient"}, {"task", "list"}} {
		_, errOut, code := h.run(args...)
		if code != 3 || !strings.Contains(errOut, "open on host laptop") || !strings.Contains(errOut, "hand fleet unlock") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if !bytes.Equal(markerBytes(t, h), before) || eventCount(t, home, "") != events || attemptStatus(t, home, dead) != "running" {
		t.Fatal("a refused open changed the fleet")
	}
	h.advance(60 * time.Second)
	if _, errOut, code := h.run("orient"); code != 3 {
		t.Fatalf("at 90s: code=%d stderr=%q", code, errOut)
	}
	h.advance(time.Second)
	if _, errOut, code := h.run("orient"); code != 0 || !strings.Contains(errOut, "took over from host laptop") {
		t.Fatalf("at 91s: code=%d stderr=%q", code, errOut)
	}
}

func TestHostMarkerStaleFromAnotherHostIsTakenOver(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	dead, alive, launching := seedLiveAttempts(t, home)
	putMarker(t, h, "laptop", "boot-x", 5*time.Minute)
	_, errOut, code := h.run("orient")
	if code != 0 || !strings.Contains(errOut, "took over from host laptop: 2 live attempts marked interrupted") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if markerBytes(t, h) != nil {
		t.Fatal("the takeover left the old marker")
	}
	if got := eventCount(t, home, "fleet.took_over"); got != 1 {
		t.Fatalf("fleet.took_over events = %d", got)
	}
	if got := eventCount(t, home, "attempt.interrupted"); got != 1 {
		t.Fatalf("attempt.interrupted events = %d", got)
	}
	if got := eventCount(t, home, "attempt.failed"); got != 1 {
		t.Fatalf("attempt.failed events = %d", got)
	}
	for id, want := range map[int64]string{dead: "interrupted", launching: "failed", alive: "running"} {
		if got := attemptStatus(t, home, id); got != want {
			t.Fatalf("attempt %d is %s, want %s", id, got, want)
		}
	}
	events := eventCount(t, home, "")
	h.ok("orient")
	if eventCount(t, home, "") != events {
		t.Fatal("a second command took over again")
	}
}

func TestHostMarkerFromAnEarlierBootOfThisHost(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "desk", "boot-a", time.Second)
	release := lockWatch(t, h.home)
	if _, errOut, code := h.run("orient"); code != 0 || strings.Contains(errOut, "took over") {
		t.Fatalf("a watcher holds the lock: code=%d stderr=%q", code, errOut)
	}
	release()
	if eventCount(t, home, "fleet.took_over") != 0 {
		t.Fatal("took over from a watcher that is alive")
	}
	if _, errOut, code := h.run("orient"); code != 0 || !strings.Contains(errOut, "took over from host desk") {
		t.Fatalf("after a reboot: code=%d stderr=%q", code, errOut)
	}
	if eventCount(t, home, "fleet.took_over") != 1 || markerBytes(t, h) != nil {
		t.Fatal("the reboot was not recorded")
	}
}

func TestHostMarkerReadsTheWindowsBootForm(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "pc", "ticks:1799999000")
	putMarker(t, h, "pc", "ticks:1799999003", 10*time.Minute)
	if _, errOut, code := h.run("orient"); code != 0 || strings.Contains(errOut, "took over") {
		t.Fatalf("same boot within jitter: code=%d stderr=%q", code, errOut)
	}
	putMarker(t, h, "pc", "ticks:1799995400", 10*time.Minute)
	release := lockWatch(t, h.home)
	if _, errOut, code := h.run("orient"); code != 0 || strings.Contains(errOut, "took over") {
		t.Fatalf("the clock jumped under a live watcher: code=%d stderr=%q", code, errOut)
	}
	release()
	if _, errOut, code := h.run("orient"); code != 0 || !strings.Contains(errOut, "took over") {
		t.Fatalf("a different boot with no watcher: code=%d stderr=%q", code, errOut)
	}
	if eventCount(t, home, "fleet.took_over") != 1 {
		t.Fatal("expected one takeover")
	}
}

func TestSupervisorStopNeverTakesOver(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Minute)
	before := markerBytes(t, h)
	h.run("supervisor", "stop")
	if !bytes.Equal(markerBytes(t, h), before) || eventCount(t, home, "fleet.took_over") != 0 {
		t.Fatal("supervisor stop took the fleet over")
	}
}

func TestFleetUnlock(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	if out := h.ok("fleet", "unlock"); !strings.Contains(out, "unlock: free") {
		t.Fatalf("no marker: %q", out)
	}
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	if _, errOut, code := h.run("orient"); code != 3 {
		t.Fatalf("before unlock: code=%d stderr=%q", code, errOut)
	}
	out := h.ok("fleet", "unlock")
	if !strings.Contains(out, "unlock: cleared") || !strings.Contains(out, "host: laptop") {
		t.Fatalf("unlock = %q", out)
	}
	if markerBytes(t, h) != nil || eventCount(t, home, "fleet.unlocked") != 1 {
		t.Fatal("unlock left the marker or wrote no event")
	}
	if _, errOut, code := h.run("orient"); code != 0 {
		t.Fatalf("after unlock: code=%d stderr=%q", code, errOut)
	}
}

func TestFleetUnlockRefusesWhileThisHostsWatcherHoldsIt(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "desk", "boot-b", time.Second)
	release := lockWatch(t, h.home)
	defer release()
	before := markerBytes(t, h)
	if _, errOut, code := h.run("fleet", "unlock"); code != 3 || !strings.Contains(errOut, "this host's watcher") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !bytes.Equal(markerBytes(t, h), before) || eventCount(t, home, "fleet.unlocked") != 0 {
		t.Fatal("a refused unlock changed the fleet")
	}
	putMarker(t, h, "laptop", "boot-x", time.Second)
	h.ok("fleet", "unlock")
}

func TestDoctorRowsForTheHostMarker(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	hasRow := func(out, row string) bool {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), row+",") {
				return true
			}
		}
		return false
	}
	out, _, _ := h.run("doctor")
	if hasRow(out, "open on another host") || hasRow(out, "unclean close") {
		t.Fatalf("a normal fleet shows a host row:\n%s", out)
	}
	events := eventCount(t, home, "")
	for _, c := range []struct {
		name, host, boot string
		age              time.Duration
		lock             bool
		row              string
		code             int
	}{
		{"fresh elsewhere", "laptop", "boot-x", 20 * time.Second, false, "open on another host", 1},
		{"stale elsewhere", "laptop", "boot-x", 10 * time.Minute, false, "unclean close", 0},
		{"this host's watcher died", "desk", "boot-b", time.Hour, false, "unclean close", 0},
		{"this host's earlier boot", "desk", "boot-a", time.Hour, false, "unclean close", 0},
		{"this host's watcher runs", "desk", "boot-b", time.Hour, true, "", 0},
	} {
		putMarker(t, h, c.host, c.boot, c.age)
		before := markerBytes(t, h)
		release := func() {}
		if c.lock {
			release = lockWatch(t, h.home)
		}
		out, _, code := h.run("doctor")
		release()
		if c.row != "" && !hasRow(out, c.row) || c.row == "" && (hasRow(out, "open on another host") || hasRow(out, "unclean close")) {
			t.Fatalf("%s: want row %q:\n%s", c.name, c.row, out)
		}
		if (code != 0) != (c.code != 0) && c.row == "open on another host" {
			t.Fatalf("%s: doctor exit %d", c.name, code)
		}
		if !bytes.Equal(markerBytes(t, h), before) || eventCount(t, home, "") != events {
			t.Fatalf("%s: doctor wrote", c.name)
		}
	}
}

func TestBeatHostKeepsOneMarkerAndNoticesATakeover(t *testing.T) {
	h, _, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	env := h.env(&bytes.Buffer{}, &bytes.Buffer{})
	if err := cli.BeatHost(env, h.home); err != nil {
		t.Fatal(err)
	}
	read := func() fleet.HostMarker {
		m, found, err := fleet.ReadHost(h.home)
		if err != nil || !found {
			t.Fatalf("marker: found=%v err=%v", found, err)
		}
		return m
	}
	first := read()
	if first.Host != "desk" || first.Boot != "boot-b" || first.PID != os.Getpid() || !first.At.Equal(h.clock()) {
		t.Fatalf("marker = %+v", first)
	}
	h.advance(8 * time.Hour)
	if err := cli.BeatHost(env, h.home); err != nil {
		t.Fatalf("after a suspend: %v", err)
	}
	if got := read(); !got.At.Equal(h.clock()) {
		t.Fatalf("the beat after a suspend kept At = %v", got.At)
	}
	putMarker(t, h, "desk", "boot-a", time.Second)
	if err := cli.BeatHost(env, h.home); err != nil {
		t.Fatalf("a marker of an earlier boot: %v", err)
	}
	if got := read(); got.Boot != "boot-b" {
		t.Fatalf("marker = %+v", got)
	}
	putMarker(t, h, "laptop", "boot-x", time.Second)
	if err := cli.BeatHost(env, h.home); err == nil || !strings.Contains(err.Error(), "took this fleet over") {
		t.Fatalf("beat over another host's marker = %v", err)
	}
	if got := read(); got.Host != "laptop" {
		t.Fatalf("a lost beat overwrote the marker: %+v", got)
	}
	cli.ReleaseHost(env, h.home)
	if read().Host != "laptop" {
		t.Fatal("release removed another host's marker")
	}
	if err := fleet.WriteHost(h.home, fleet.HostMarker{Host: "desk", Boot: "boot-b", PID: os.Getpid() + 1, At: h.clock()}); err != nil {
		t.Fatal(err)
	}
	cli.ReleaseHost(env, h.home)
	if markerBytes(t, h) == nil {
		t.Fatal("release removed a marker of another process")
	}
}

func TestWatchHoldsTheMarkerAndStopsWhenAnotherHostTakesOver(t *testing.T) {
	asHost(t, "desk", "boot-b")
	old := *cli.HostBeat
	*cli.HostBeat = 20 * time.Millisecond
	t.Cleanup(func() { *cli.HostBeat = old })
	fx := newAttemptFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var errOut string
	go func() {
		_, e, code := fx.h.runCtx(ctx, "watch", "--every", "1h", "--notify=false")
		errOut = e
		done <- code
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	read := func() fleet.HostMarker {
		m, _, _ := fleet.ReadHost(fx.h.home)
		return m
	}
	eventually(t, func() bool { return read().PID == os.Getpid() })
	m := read()
	if m.Host != "desk" || m.Boot != "boot-b" {
		t.Fatalf("marker = %+v", m)
	}
	fx.h.advance(time.Minute)
	eventually(t, func() bool { return read().At.Equal(fx.h.clock()) })
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(5 * time.Second)
	for finished := false; !finished; {
		select {
		case code := <-done:
			if code != 3 || !strings.Contains(errOut, "took this fleet over") {
				t.Fatalf("code=%d stderr=%q", code, errOut)
			}
			finished = true
		case <-tick.C:
			putMarker(t, fx.h, "laptop", "boot-x", 0)
		case <-deadline:
			t.Fatal("the watcher kept running after another host took the fleet")
		}
	}
	if read().Host != "laptop" {
		t.Fatalf("the watcher touched the new owner's marker: %+v", read())
	}
}

func TestWatchClearsItsMarkerOnAStop(t *testing.T) {
	asHost(t, "desk", "boot-b")
	fx := newAttemptFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		_, _, code := fx.h.runCtx(ctx, "watch", "--every", "1h", "--notify=false")
		done <- code
	}()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	eventually(t, func() bool { return markerBytes(t, fx.h) != nil })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not stop")
	}
	if markerBytes(t, fx.h) != nil {
		t.Fatal("a clean stop left fleet.host")
	}
}

func writeBootedPID(t *testing.T, path string, pid int, marker, id, host string) {
	t.Helper()
	line := strconv.Itoa(pid) + " " + marker + " " + selfExe(t) + "\nboot " + id + " " + host + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStopNeverSignalsAPidFromAnotherBoot(t *testing.T) {
	if boot.Stable() == "" {
		t.Skip("this system writes no boot id into pid files")
	}
	h, _, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.home, "watch.pid")
	writeBootedPID(t, path, os.Getpid(), marker, "00000000-0000-4000-8000-000000000000", "desk")
	if _, _, _, live := update.LivePID(path); live {
		t.Fatal("a pid of another boot reads as live")
	}
	out, _, code := h.run("watch", "stop")
	if code != 0 || !strings.Contains(out, "watcher: not running") || !strings.Contains(out, "another host or boot") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the stale pid file of an earlier boot stayed: %v", err)
	}
}

func TestStopLeavesThePidFileOfAnotherHost(t *testing.T) {
	if boot.Stable() == "" {
		t.Skip("this system writes no boot id into pid files")
	}
	h, _, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.home, "watch.pid")
	writeBootedPID(t, path, os.Getpid(), marker, "00000000-0000-4000-8000-000000000000", "laptop")
	out, _, code := h.run("watch", "stop")
	if code != 0 || !strings.Contains(out, "watcher: not running") || !strings.Contains(out, "host laptop") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("another host's pid file was removed: %v", err)
	}
}

func TestHostFileHoldsPlainJSON(t *testing.T) {
	h, _, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	if err := cli.BeatHost(h.env(&bytes.Buffer{}, &bytes.Buffer{}), h.home); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(markerBytes(t, h), &m); err != nil || m["host"] != "desk" || m["boot"] != "boot-b" || int(m["pid"].(float64)) != os.Getpid() || m["at"] == nil {
		t.Fatalf("fleet.host = %v, %v", m, err)
	}

}

func backupNames(t *testing.T, home string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(home, "backups"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRefusedOpenOfACopyChangesNothing(t *testing.T) {
	h, old, copied := adoptCopy(t)
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	events, backups, marker := eventCount(t, copied, ""), backupNames(t, copied), markerBytes(t, h)
	_, errOut, code := h.run("orient")
	if code != 3 || !strings.Contains(errOut, "open on host laptop") || strings.Contains(errOut, "adopted") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("a refused open rewrote the recorded home to %q", got)
	}
	if got := eventCount(t, copied, ""); got != events {
		t.Fatalf("a refused open wrote %d events", got-events)
	}
	if got := backupNames(t, copied); !slices.Equal(got, backups) {
		t.Fatalf("backups %q, were %q", got, backups)
	}
	if !bytes.Equal(markerBytes(t, h), marker) {
		t.Fatal("a refused open rewrote the marker")
	}
}

func TestSupervisorStopOfAnUnadoptedCopyJudgesTheHost(t *testing.T) {
	h, old, copied := adoptCopy(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	for _, args := range [][]string{{"supervisor", "stop"}, {"supervisor", "interrupt"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "open on host laptop") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := recordedHome(t, copied); got != old {
		t.Fatalf("a refused command changed the database: %q", got)
	}
}

func TestUnreadableHostMarkerWarnsAndUnlockRemovesIt(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced here")
	}
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	path := filepath.Join(h.home, fleet.HostFile)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("orient"); code != 0 || !strings.Contains(errOut, "cannot read") || !strings.Contains(errOut, "treating it as absent") {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("fleet", "unlock"); !strings.Contains(out, "unlock: cleared") {
		t.Fatalf("unlock = %q", out)
	}
	if markerBytes(t, h) != nil || eventCount(t, home, "fleet.unlocked") != 1 {
		t.Fatal("unlock left the unreadable marker or wrote no event")
	}
}

func TestInitRefusesAFleetAnotherHostHasOpen(t *testing.T) {
	h, home, _ := initMoveFleet(t)
	asHost(t, "desk", "boot-b")
	putMarker(t, h, "laptop", "boot-x", 10*time.Second)
	before, events := markerBytes(t, h), eventCount(t, home, "")
	if _, errOut, code := h.run("init"); code != 3 || !strings.Contains(errOut, "open on host laptop") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !bytes.Equal(markerBytes(t, h), before) || eventCount(t, home, "") != events {
		t.Fatal("a refused init changed the fleet")
	}
}

func TestBeatHostToleratesWindowsTickDriftAfterARename(t *testing.T) {
	h, _, _ := initMoveFleet(t)
	env := h.env(&bytes.Buffer{}, &bytes.Buffer{})
	asHost(t, "pc", "ticks:1799999000")
	if err := cli.BeatHost(env, h.home); err != nil {
		t.Fatal(err)
	}
	asHost(t, "pc-renamed", "ticks:1799999003")
	if err := cli.BeatHost(env, h.home); err != nil {
		t.Fatalf("a renamed host with drifted ticks lost its own marker: %v", err)
	}
	if _, errOut, code := h.run("orient"); code != 0 {
		t.Fatalf("orient: code=%d stderr=%q", code, errOut)
	}
}
