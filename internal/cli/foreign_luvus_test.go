package cli_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const otherSocket = "/elsewhere/luvus.sock"

func recordSocket(t *testing.T, h *harness, table, socket string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE "+table+" SET socket = ?", socket); err != nil {
		t.Fatal(err)
	}
}

func TestAnAttemptOfAnotherLuvusIsRecordedAndNeverSignalled(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	recordSocket(t, fx.h, "attempt", otherSocket)
	fx.rt.srv.SetGeneration("gen-2")
	show := fx.h.ok("attempt", "show", "a1")
	for _, want := range []string{"status: interrupted", "terminal belongs to another luvus (" + otherSocket + ")", "recorded by"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show = %q, missing %q", show, want)
		}
	}
	if strings.Contains(show, "requested by") {
		t.Fatalf("show = %q", show)
	}
	if gone(pid) {
		t.Fatalf("worker %d of another luvus was signalled", pid)
	}
}

func TestAnAttemptOfThisLuvusIsStillStoppedAfterARestart(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	fx.rt.srv.SetGeneration("gen-2")
	show := fx.h.ok("attempt", "show", "a1")
	for _, want := range []string{"status: interrupted", "luvus server restarted", "requested by"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show = %q, missing %q", show, want)
		}
	}
	if !gone(pid) {
		t.Fatalf("worker %d survived the restart", pid)
	}
}

func TestAnAttemptFromBeforeTheSocketWasRecordedKeepsTheOldBehaviour(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	recordSocket(t, fx.h, "attempt", "")
	fx.rt.srv.SetGeneration("gen-2")
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "luvus server restarted") {
		t.Fatalf("show = %q", show)
	}
	if !gone(pid) {
		t.Fatalf("worker %d survived the restart", pid)
	}
}

func TestAnExitedWorkerIsRecordedNotRequested(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.exitAll()
	show := fx.h.ok("attempt", "show", "a1")
	if !strings.Contains(show, "recorded by") || strings.Contains(show, "requested by") {
		t.Fatalf("show = %q", show)
	}
}

func TestASupervisorOfAnotherLuvusIsRecordedAndNeverSignalled(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	pid := rt.lastPID()
	recordSocket(t, h, "supervisor", otherSocket)
	rt.srv.SetGeneration("gen-2")
	show := h.ok("supervisor", "show")
	has(t, "show", show, "status: interrupted", "terminal belongs to another luvus ("+otherSocket+")", "recorded by")
	if strings.Contains(show, "requested by") {
		t.Fatalf("show = %q", show)
	}
	if gone(pid) {
		t.Fatalf("supervisor %d of another luvus was signalled", pid)
	}
}

func TestASupervisorOfThisLuvusIsStillStoppedAfterARestart(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	pid := rt.lastPID()
	rt.srv.SetGeneration("gen-2")
	has(t, "show", h.ok("supervisor", "show"), "status: interrupted", "luvus server restarted", "requested by")
	if !gone(pid) {
		t.Fatalf("supervisor %d survived the restart", pid)
	}
}

func storedSocket(t *testing.T, h *harness, table string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var socket string
	if err := db.QueryRow("SELECT socket FROM " + table + " ORDER BY id DESC LIMIT 1").Scan(&socket); err != nil {
		t.Fatal(err)
	}
	return socket
}

func spellings(t *testing.T, socket string) map[string]string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("socket paths are not files on Windows")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(socket), link); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"uncleaned": filepath.Dir(socket) + "/./" + filepath.Base(socket),
		"symlinked": filepath.Join(link, filepath.Base(socket)),
	}
}

func TestAnotherPathToTheSameSocketIsNotForeign(t *testing.T) {
	for _, name := range []string{"uncleaned", "symlinked"} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			fx.start()
			pid := fx.rt.lastPID()
			recordSocket(t, fx.h, "attempt", spellings(t, fx.h.vars["HAND_LUVUS_SOCKET"])[name])
			fx.rt.srv.SetGeneration("gen-2")
			show := fx.h.ok("attempt", "show", "a1")
			if !strings.Contains(show, "luvus server restarted") || strings.Contains(show, "another luvus") {
				t.Fatalf("show = %q", show)
			}
			if !gone(pid) {
				t.Fatalf("worker %d survived the restart", pid)
			}
		})
	}
}

func TestTheLaunchRecordsTheSocketOfItsLuvus(t *testing.T) {
	fx := newAttemptFixture(t)
	want := fx.h.vars["HAND_LUVUS_SOCKET"]
	fx.start()
	if got := storedSocket(t, fx.h, "attempt"); got != want {
		t.Fatalf("attempt socket = %q, want %q", got, want)
	}
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	if got, want := storedSocket(t, h, "supervisor"), rt.srv.Socket; got != want {
		t.Fatalf("supervisor socket = %q, want %q", got, want)
	}
}

func TestASwitchRecordsTheSocketOfItsLuvus(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	want := fx.h.vars["HAND_LUVUS_SOCKET"]
	recordSocket(t, fx.h, "attempt", filepath.Dir(want)+"/./"+filepath.Base(want))
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m2")
	if got := storedSocket(t, fx.h, "attempt"); got != want {
		t.Fatalf("socket after the switch = %q, want %q", got, want)
	}
}

func TestAHealthyRowFromBeforeTheSocketWasRecordedGetsThisLuvusSocket(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	recordSocket(t, fx.h, "attempt", "")
	fx.h.ok("attempt", "show", "a1")
	if got, want := storedSocket(t, fx.h, "attempt"), fx.h.vars["HAND_LUVUS_SOCKET"]; got != want {
		t.Fatalf("attempt socket = %q, want %q", got, want)
	}
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	recordSocket(t, h, "supervisor", "")
	h.ok("supervisor", "show")
	if got := storedSocket(t, h, "supervisor"); got != rt.srv.Socket {
		t.Fatalf("supervisor socket = %q, want %q", got, rt.srv.Socket)
	}
}

func TestCleaningAnAttemptOfAnotherLuvusIsRefused(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	pid := fx.rt.lastPID()
	recordSocket(t, fx.h, "attempt", otherSocket)
	fx.rt.srv.SetGeneration("gen-2")
	fx.h.ok("attempt", "show", "a1")
	_, errOut, code := fx.h.run("attempt", "clean", "--discard", "a1")
	if code != 3 || !strings.Contains(errOut, "belongs to another luvus ("+otherSocket+")") || !strings.Contains(errOut, "nothing was removed") {
		t.Fatalf("clean code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(fx.h.worktree("t1-a1")); err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if gone(pid) {
		t.Fatalf("worker %d of another luvus was signalled", pid)
	}
}
