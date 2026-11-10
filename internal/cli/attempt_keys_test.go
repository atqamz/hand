package cli_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "claude-2.1.316-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAttemptKeysPressesTheKeysOnAClaudePermissionScreenForAnyAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 7, capture(t, "commit") })
	out := fx.h.ok("attempt", "keys", "--revision", "7", "a1", "1")
	if !strings.Contains(out, "keys: 1") || strings.Contains(out, "grant") {
		t.Fatalf("keys = %q", out)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 8, capture(t, "commit") })
	fx.h.ok("attempt", "keys", "--revision", "8", "a1", "4")
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"1", "4"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func TestAttemptKeysPressesAnyKeyOnATrustScreenOfAnotherFolder(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen, rt.status = 7, capture(t, "trust"), "blocked" })
	fx.h.ok("attempt", "keys", "--revision", "7", "a1", "esc")
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"esc"}) {
		t.Fatalf("keys sent = %q", got)
	}
}

func legacyAttemptRow(t *testing.T, fx *attemptFixture) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(fx.h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE attempt SET approve = 'ask', access = 'readonly'`); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptShowPrintsNoApproveOrAccessEvenForALegacyRow(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	legacyAttemptRow(t, fx)
	show := fx.h.ok("attempt", "show", "a1")
	if strings.Contains(show, "approve") || strings.Contains(show, "access") {
		t.Fatalf("show = %q", show)
	}
	if list := fx.h.ok("attempt", "list"); !strings.Contains(list, "a1") {
		t.Fatalf("list = %q", list)
	}
}

func TestProjectGrantsIsAnUnknownCommand(t *testing.T) {
	h := initWithProject(t)
	_, errOut, code := h.run("project", "grants", "hand")
	if code != 2 || !strings.Contains(errOut, "grants") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestWaitDoesNotWakeOnTheRemovedDeniedEvent(t *testing.T) {
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "start", "t1")
	db, err := sql.Open("sqlite", filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	removed := "attempt." + "denied"
	if _, err := db.Exec(`INSERT INTO event(at, kind, task_id, detail) VALUES ('2026-09-26T00:00:00Z', ?, 1, 'a1: x')`, removed); err != nil {
		t.Fatal(err)
	}
	if got := h.ok("wait", "--after", "0", "--timeout", "1ms"); strings.Contains(got, removed) {
		t.Fatalf("wait = %q", got)
	}
}
