package faketool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runTreehouse(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	c := exec.Command("treehouse", args...)
	c.Dir = dir
	var out, errOut strings.Builder
	c.Stdout = &out
	c.Stderr = &errOut
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run treehouse %v: %v", args, err)
	}
	return out.String(), errOut.String(), code
}

// A pool of one hands its slot out once, refuses while it is out, and hands it
// back out under a fresh identity once returned - the three answers a fake that
// ignores its own state collapses into one.
func TestTreehouseLeasesEachSlotOnceUntilItIsReturned(t *testing.T) {
	bin := Bin(t)
	slot := filepath.Join(t.TempDir(), "wt")
	Treehouse{Slots: []string{slot}}.Install(t, bin)

	first, banner, code := runTreehouse(t, t.TempDir(), "get", "--lease", "--json")
	if code != 0 || !strings.Contains(first, `"path":"`+slot+`"`) {
		t.Fatalf("get = %q (exit %d), want the slot leased", first, code)
	}
	if !strings.Contains(banner, TreehouseBanner) {
		t.Fatalf("stderr = %q, want the banner off stdout where the JSON is", banner)
	}

	_, exhausted, code := runTreehouse(t, t.TempDir(), "get", "--lease", "--json")
	if code != 1 || !strings.Contains(exhausted, "in use or dirty") {
		t.Fatalf("second get = %q (exit %d), want the pool reported exhausted", exhausted, code)
	}

	if _, _, code := runTreehouse(t, t.TempDir(), "return", slot); code != 0 {
		t.Fatalf("return exit %d, want 0", code)
	}
	second, _, code := runTreehouse(t, t.TempDir(), "get", "--lease", "--json")
	if code != 0 {
		t.Fatalf("get after return exit %d, want the freed slot handed back out", code)
	}
	if second == first {
		t.Fatalf("both leases = %q, want a fresh identity: a recycled slot keeps its path and never its lease id", second)
	}
}

func TestTreehouseReturnsIdempotentlyAndRefusesAnUnmanagedPath(t *testing.T) {
	bin := Bin(t)
	slot := filepath.Join(t.TempDir(), "wt")
	Treehouse{Slots: []string{slot}}.Install(t, bin)

	for i := range 2 {
		if _, errOut, code := runTreehouse(t, t.TempDir(), "return", slot); code != 0 {
			t.Fatalf("return %d exit %d (%q), want a repeated return to succeed", i+1, code, errOut)
		}
	}
	_, errOut, code := runTreehouse(t, t.TempDir(), "return", filepath.Join(t.TempDir(), "elsewhere"))
	if code != 1 || !strings.Contains(errOut, "not managed by treehouse") {
		t.Fatalf("unmanaged return = %q (exit %d), want a refusal", errOut, code)
	}
}

// The recorded behaviour internal/worktree.Return has to survive: the prompt gets
// no answer, treehouse aborts, and it still exits 0 with the slot still leased.
func TestTreehouseAbortsAnUnforcedDirtyReturnWithExitZero(t *testing.T) {
	bin := Bin(t)
	slot := filepath.Join(t.TempDir(), "wt")
	InitRepo(t, slot)
	Treehouse{Slots: []string{slot}}.Install(t, bin)

	if _, _, code := runTreehouse(t, t.TempDir(), "get", "--lease", "--json"); code != 0 {
		t.Fatal("get failed")
	}
	if err := os.WriteFile(filepath.Join(slot, "dirt.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := runTreehouse(t, t.TempDir(), "return", slot)
	if code != 0 {
		t.Fatalf("aborted return exit %d, want 0: the exit code is exactly what does not report this", code)
	}
	if !strings.Contains(errOut, "Aborted") {
		t.Fatalf("stderr = %q, want the abort reported", errOut)
	}
	if _, _, code := runTreehouse(t, t.TempDir(), "get", "--lease", "--json"); code != 1 {
		t.Fatalf("get after an aborted return exit %d, want the slot still leased", code)
	}

	if _, _, code := runTreehouse(t, t.TempDir(), "return", slot, "--force"); code != 0 {
		t.Fatal("forced return of a dirty worktree failed")
	}
	if out, err := exec.Command("git", "-C", slot, "status", "--porcelain").Output(); err != nil || len(out) != 0 {
		t.Fatalf("worktree still dirty after a forced return: %q %v", out, err)
	}
}

func TestTreehouseHeldSlotStartsLeasedAndIsReturnable(t *testing.T) {
	bin := Bin(t)
	held := filepath.Join(t.TempDir(), "wt-seeded")
	Treehouse{Held: []string{held}}.Install(t, bin)

	if _, _, code := runTreehouse(t, t.TempDir(), "return", held); code != 0 {
		t.Fatalf("return of a seeded lease exit %d, want 0", code)
	}
}
