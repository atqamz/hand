package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
)

func TestTwoFleetsSharingARepoKeepTheirWorkersApart(t *testing.T) {
	luvusHome, err := os.MkdirTemp("", "lv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(luvusHome) })
	root := t.TempDir()
	repo := gitRepo(t)
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("Fix it."), 0o644); err != nil {
		t.Fatal(err)
	}
	type side struct {
		h  *harness
		rt *fakeRuntime
		id string
	}
	open := func() side {
		h := newHarness(t)
		h.vars["SECONDHAND_HOME"] = root
		h.vars["LUVUS_HOME"] = luvusHome
		h.vars["PATH"] = fakeBin(t)
		h.vars["HOME"] = t.TempDir()
		id := field(h.ok("init"), "id")
		sock := luvus.SocketPath(func(k string) string { return h.vars[k] }, fleet.Session(id))
		if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
			t.Fatal(err)
		}
		rt := startRuntime(t, sock)
		h.ok("project", "add", "app", repo)
		h.ok("task", "add", "app", "Fix login")
		h.ok("task", "start", "t1")
		out := h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", brief, "t1")
		if !strings.Contains(out, "luvus session attach "+fleet.Session(id)) || !strings.Contains(out, "branch: hand/"+id+"/t1-a1") {
			t.Fatalf("start help = %q", out)
		}
		return side{h, rt, id}
	}
	a, b := open(), open()
	if a.id == b.id {
		t.Fatal("two fleets share an id")
	}
	if got, want := a.rt.lastCreate().CWD, filepath.Join(root, "worktrees", a.id, "t1-a1"); got != want {
		t.Fatalf("fleet a worktree = %q, want %q", got, want)
	}
	if got, want := b.rt.lastCreate().CWD, filepath.Join(root, "worktrees", b.id, "t1-a1"); got != want {
		t.Fatalf("fleet b worktree = %q, want %q", got, want)
	}
	a.h.ok("attempt", "stop", "a1")
	if show := b.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: running") {
		t.Fatalf("stopping fleet a's a1 touched fleet b: %q", show)
	}
}

func TestReportFromAWorktreeOfAnUnregisteredFleetIsRefused(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.cwd = fx.h.worktree("t1-a1")
	id := filepath.Base(filepath.Dir(fx.h.cwd))
	if err := os.Remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "fleets", id)); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := fx.h.run("report", "add", "--status", "done", "--text", "x"); code != 2 || !strings.Contains(errOut, "no fleet "+id+" is registered") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestReportFromAWorktreeRefusesAnotherSelectedFleet(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	other := t.TempDir()
	fx.h.ok("init", other)
	fx.h.cwd = fx.h.worktree("t1-a1")
	for _, args := range [][]string{{"--home", other, "report", "add", "--status", "done", "--text", "x"}} {
		if _, errOut, code := fx.h.run(args...); code != 3 || !strings.Contains(errOut, "belongs to the fleet at "+fx.h.home) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	fx.h.vars["HAND_HOME"] = other
	if _, errOut, code := fx.h.run("report", "add", "--status", "done", "--text", "x"); code != 3 || !strings.Contains(errOut, "belongs to the fleet at "+fx.h.home) {
		t.Fatalf("HAND_HOME: code=%d stderr=%q", code, errOut)
	}
	fx.h.vars["HAND_HOME"] = ""
	if out := fx.h.ok("report", "add", "--status", "done", "--text", "mine"); !strings.Contains(out, "attempt: a1") {
		t.Fatalf("report without a selected home = %q", out)
	}
}

func TestLuvusStartsInTheFleetsOwnUserUnit(t *testing.T) {
	h := initWithProject(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\necho \"$*\" >> " + calls + "\necho 'Failed to start transient service unit' >&2\nexit 1\n",
		"systemctl":   "#!/bin/sh\nexit 1\n",
		"luvus":       "#!/bin/sh\necho \"LUVUS-SPAWNED-DIRECTLY $*\" >> " + calls + "\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := t.TempDir()
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.vars["PATH"], h.vars["XDG_RUNTIME_DIR"] = bin, run
	h.vars["HAND_LUVUS_SOCKET"] = filepath.Join(t.TempDir(), "absent.sock")
	id := regexp.MustCompile(`\((f[0-9a-f]{12})\)`).FindStringSubmatch(h.ok("orient"))[1]
	if _, errOut, code := h.run("attempt", "list"); code == 0 || !strings.Contains(errOut, "secondhand-luvus-"+id) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), "--unit=secondhand-luvus-"+id+" ") || strings.Contains(string(got), "LUVUS-SPAWNED-DIRECTLY") {
		t.Fatalf("calls = %q", got)
	}
}
