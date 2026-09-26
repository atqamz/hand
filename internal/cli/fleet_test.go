package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetListShowsEveryRegisteredFleet(t *testing.T) {
	h := newHarness(t)
	alpha := field(h.ok("init", "--name", "alpha"), "id")
	other := t.TempDir()
	beta := field(h.ok("init", "--name", "beta", other), "id")
	gone := t.TempDir()
	dead := field(h.ok("init", gone), "id")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	h.cwd = t.TempDir()
	h.vars["HAND_HOME"] = ""
	out := h.ok("fleet", "list")
	for _, want := range []string{"fleets[3]{id,name,home,state}:", alpha + ",alpha," + h.home + ",ok", beta + ",beta," + other + ",ok", dead + `,"",` + gone + ",missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet list missing %q:\n%s", want, out)
		}
	}
}

func TestInitAfterAMoveRepairsWorktreesOfProjectsInsideTheHome(t *testing.T) {
	fx := newAttemptFixture(t)
	inner := filepath.Join(fx.h.home, "projects", "inner")
	if err := os.MkdirAll(filepath.Dir(inner), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(gitRepo(t), inner); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("project", "add", "inner", inner)
	fx.h.ok("task", "add", "inner", "Inner work")
	fx.h.ok("task", "start", "t2")
	wt := field(fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t2"), "worktree")
	fx.h.ok("attempt", "stop", "a1")
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(fx.h.home, moved); err != nil {
		t.Fatal(err)
	}
	fx.h.home = moved
	if out, err := exec.Command("git", "-C", wt, "status", "--short").CombinedOutput(); err == nil {
		t.Fatalf("worktree still works before repair: %s", out)
	}
	fx.h.ok("init")
	if out, err := exec.Command("git", "-C", wt, "status", "--short").CombinedOutput(); err != nil {
		t.Fatalf("worktree broken after init: %v: %s", err, out)
	}
	if list := fx.h.ok("project", "list"); !strings.Contains(list, "inner,"+filepath.Join(moved, "projects", "inner")) {
		t.Fatalf("project list = %q", list)
	}
}
