package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func withLuvus(t *testing.T, h *harness) string {
	t.Helper()
	bin := filepath.Join(h.vars["PATH"], "luvus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.tty = true
	return bin
}

func fleetID(t *testing.T, h *harness) string {
	t.Helper()
	return regexp.MustCompile(`\((f[0-9a-f]{12})\)`).FindStringSubmatch(h.ok("orient"))[1]
}

func TestAttachSupervisorOpensItsPane(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	luvus := withLuvus(t, h)
	has(t, "start", startClaudeSupervisor(h), "hand attach supervisor")
	h.ok("attach", "supervisor")
	want := []string{luvus, luvus, "--session", "secondhand-" + fleetID(t, h), "attach", "2"}
	if len(h.exec) != 1 || !slices.Equal(h.exec[0], want) {
		t.Fatalf("exec = %q, want %q", h.exec, want)
	}
}

func TestAttachAnAttemptOpensItsPane(t *testing.T) {
	fx := newAttemptFixture(t)
	luvus := withLuvus(t, fx.h)
	has(t, "start", fx.start(), "hand attach a1")
	fx.h.ok("attach", "a1")
	if len(fx.h.exec) != 1 || !slices.Equal(fx.h.exec[0], []string{luvus, luvus, "--session", "secondhand-" + fleetID(t, fx.h), "attach", "2"}) {
		t.Fatalf("exec = %q", fx.h.exec)
	}
}

func TestAttachWithNoTargetOpensTheWholeSession(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	luvus := withLuvus(t, h)
	h.ok("attach")
	if len(h.exec) != 1 || !slices.Equal(h.exec[0], []string{luvus, luvus, "session", "attach", "secondhand-" + fleetID(t, h)}) {
		t.Fatalf("exec = %q", h.exec)
	}
}

func TestAttachRefusals(t *testing.T) {
	fx := newAttemptFixture(t)
	withLuvus(t, fx.h)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"attach", "supervisor"}, 3, "hand supervisor start"},
		{[]string{"attach", "a9"}, 3, "a9"},
		{[]string{"attach", "t1"}, 2, "supervisor or an attempt"},
		{[]string{"attach", "a1", "a2"}, 2, "attach"},
	} {
		if _, errOut, code := fx.h.run(c.args...); code != c.code || !strings.Contains(errOut, c.want) {
			t.Fatalf("%q: code=%d stderr=%q", c.args, code, errOut)
		}
	}
	fx.start()
	fx.h.tty = false
	if _, errOut, code := fx.h.run("attach", "a1"); code != 2 || !strings.Contains(errOut, "terminal") {
		t.Fatalf("no terminal: code=%d stderr=%q", code, errOut)
	}
	if len(fx.h.exec) != 0 {
		t.Fatalf("exec = %q", fx.h.exec)
	}
}

func TestAttachRefusesATargetWithNoPane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	withLuvus(t, h)
	rt.set(func(rt *fakeRuntime) { rt.blankPane = true })
	startClaudeSupervisor(h)
	if _, errOut, code := h.run("attach", "supervisor"); code != 3 || !strings.Contains(errOut, "no pane") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if len(h.exec) != 0 {
		t.Fatalf("exec = %q, want no whole-session fallback", h.exec)
	}
}
