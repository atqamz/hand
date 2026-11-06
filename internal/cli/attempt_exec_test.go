package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

var scratchNames = []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR", "CLAUDE_CODE_TMPDIR"}

func scratchOf(h *harness, worktree string) string {
	root, err := filepath.EvalSymlinks(h.vars["SECONDHAND_HOME"])
	if err != nil {
		root = h.vars["SECONDHAND_HOME"]
	}
	return filepath.Join(root, "scratch", filepath.Base(filepath.Dir(worktree)), filepath.Base(worktree))
}

func readScratchEnv(t *testing.T, path string) (map[string]string, []string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	vars := map[string]string{}
	for _, line := range lines {
		if name, value, ok := strings.Cut(line, "="); ok && name != "argv" {
			vars[name] = value
		}
	}
	var argv []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[len(lines)-1], "argv=")), &argv); err != nil {
		t.Fatal(err)
	}
	return vars, argv
}

func TestAttemptLaunchUsesTheScratchWrapper(t *testing.T) {
	fx := newAttemptFixture(t)
	t.Setenv("HAND_TEST_AS_HAND", "1")
	t.Setenv("TMPDIR", "/tmp")
	out := filepath.Join(t.TempDir(), "env")
	dir := t.TempDir()
	fakebin.Install(t, dir, "claude", "env", map[string]string{"out": out})
	fx.h.vars["PATH"] = dir
	fx.start()
	wt := fx.h.worktree("t1-a1")
	scratch := scratchOf(fx.h, wt)
	eventually(t, func() bool { return exists(out) })
	env, argv := readScratchEnv(t, out)
	for _, name := range scratchNames {
		if env[name] != scratch {
			t.Errorf("%s = %q, want %q", name, env[name], scratch)
		}
	}
	if env["TMPDIR"] == "/tmp" || env["TMPDIR"] == "" {
		t.Errorf("TMPDIR = %q, the wrapper did not point it at the scratch folder", env["TMPDIR"])
	}
	if env["HOME"] != os.Getenv("HOME") {
		t.Errorf("HOME = %q, the wrapper changed it", env["HOME"])
	}
	harness := fx.rt.lastCreate().Command
	if !slices.Equal(argv, harness[1:]) {
		t.Errorf("harness argv = %q, want %q", argv, harness[1:])
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sent := fx.rt.lastSent().Command
	want := append([]string{self, "attempt", "_exec", "--dir", scratch, "--"}, harness...)
	if !slices.Equal(sent, want) {
		t.Errorf("Luvus command = %q, want %q", sent, want)
	}
	info, err := os.Stat(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("scratch mode = %o, want 700", info.Mode().Perm())
	}
}

func TestAttemptExecPassesStdinAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the passthrough check needs a POSIX shell")
	}
	cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", t.TempDir(), "--", "/bin/sh", "-c", "cat; exit 7")
	cmd.Env = append(os.Environ(), "HAND_TEST_AS_HAND=1")
	cmd.Stdin = strings.NewReader("through")
	out, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 7 {
		t.Fatalf("exit = %v, want 7; output %q", err, out)
	}
	if string(out) != "through" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestHiddenExecIsNotInHelpOrAttemptList(t *testing.T) {
	fx := newAttemptFixture(t)
	if _, errOut, code := fx.h.run("attempt"); code != 2 || strings.Contains(errOut, "_exec") {
		t.Fatalf("attempt help: code=%d stderr=%q", code, errOut)
	}
	if out := fx.h.ok("attempt", "list"); strings.Contains(out, "_exec") {
		t.Fatalf("attempt list = %q", out)
	}
}

func TestCleanRemovesTheAttemptScratch(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	wt := fx.h.worktree("t1-a1")
	scratch := scratchOf(fx.h, wt)
	eventually(t, func() bool { return exists(scratch) })
	sibling := filepath.Join(filepath.Dir(scratch), "t1-a2")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("attempt", "stop", "a1")
	out := fx.h.ok("attempt", "clean", "a1")
	if !strings.Contains(out, "scratch_removed[1]{path,bytes}") || !strings.Contains(out, scratch) {
		t.Fatalf("clean = %q", out)
	}
	if exists(scratch) || !exists(sibling) {
		t.Fatalf("after clean: scratch=%v sibling=%v", exists(scratch), exists(sibling))
	}
}

func TestGCListTheAttemptScratchAndRemovesIt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	live := scratchOf(fx.h, fx.h.worktree("t1-a1"))
	eventually(t, func() bool { return exists(live) })
	orphan := filepath.Join(filepath.Dir(live), "t5-a9")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "f"), make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	out := fx.h.ok("attempt", "gc")
	if !strings.Contains(out, "orphans[1]{path,bytes}") || !strings.Contains(out, orphan+",10") || strings.Contains(out, live) {
		t.Fatalf("gc = %q", out)
	}
	out = fx.h.ok("attempt", "gc", "--apply")
	if !strings.Contains(out, "applied: true") || !strings.Contains(out, orphan) {
		t.Fatalf("gc --apply = %q", out)
	}
	if exists(orphan) || !exists(live) {
		t.Fatalf("after --apply: orphan=%v live=%v", exists(orphan), exists(live))
	}
}

func TestGCSkipsALiveAttemptsScratch(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	wt := fx.h.worktree("t1-a1")
	scratch := scratchOf(fx.h, wt)
	eventually(t, func() bool { return exists(scratch) })
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if out := fx.h.ok("attempt", "gc", "--apply"); strings.Contains(out, scratch) {
		t.Fatalf("gc = %q", out)
	}
	if !exists(scratch) {
		t.Fatal("gc removed a live attempt's scratch")
	}
	fx.h.ok("attempt", "stop", "a1")
	if out := fx.h.ok("attempt", "gc"); !strings.Contains(out, scratch) {
		t.Fatalf("gc after stop = %q", out)
	}
}
