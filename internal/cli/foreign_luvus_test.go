package cli_test

import (
	"database/sql"
	"path/filepath"
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
