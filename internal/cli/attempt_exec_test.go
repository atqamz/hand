package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var scratchNames = []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR", "CLAUDE_CODE_TMPDIR"}

func scratchOf(h *harness, worktree string) string {
	root, err := filepath.EvalSymlinks(h.home)
	if err != nil {
		root = h.home
	}
	return filepath.Join(root, "scratch", filepath.Base(worktree))
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

func TestAttemptExecRefusesAMovedFleetHome(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone", "scratch", "t1-a1")
	cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", dir, "--", "/bin/true")
	cmd.Env = append(os.Environ(), "HAND_TEST_AS_HAND=1")
	out, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 1 {
		t.Fatalf("exit = %v, want 1; output %q", err, out)
	}
	if !strings.Contains(string(out), "the fleet home for") {
		t.Fatalf("output = %q", out)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the wrapper recreated %s: %v", dir, err)
	}
}

func execScratch(t *testing.T, dir string, command ...string) (string, int) {
	t.Helper()
	if len(command) == 0 {
		command = []string{"/usr/bin/true"}
	}
	args := append([]string{os.Args[0], "attempt", "_exec", "--dir", dir, "--"}, command...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "HAND_TEST_AS_HAND=1")
	out, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return string(out), exited.ExitCode()
	}
	if err != nil {
		t.Fatalf("_exec: %v; output %q", err, out)
	}
	return string(out), 0
}

func copyFleetDB(t *testing.T, src, dst string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(filepath.Join(src, "hand.db"+suffix))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "hand.db"+suffix), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := state.Open(filepath.Join(dst, "hand.db"), time.Now)
	if err != nil {
		t.Fatalf("open the copied database: %v", err)
	}
	defer st.Close()
	if _, err := st.Attempt(context.Background(), 1); err != nil {
		t.Fatalf("the copied database has no attempt a1: %v", err)
	}
}

func TestAttemptExecRefusesEveryPathButTheAttemptsScratch(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	wt := fx.h.worktree("t1-a1")
	base := t.TempDir()
	cases := []struct {
		name string
		make func() (string, []string)
	}{
		{"an unrelated scratch folder", func() (string, []string) {
			dir := filepath.Join(base, "team", "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1"), []string{dir}
		}},
		{"a folder whose parent is not named scratch", func() (string, []string) {
			dir := filepath.Join(base, "work")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1"), []string{dir}
		}},
		{"a deeper scratch folder in the fleet home", func() (string, []string) {
			dir := filepath.Join(home, "sub", "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1"), []string{dir, filepath.Dir(dir)}
		}},
		{"another fleet home with a copy of the database", func() (string, []string) {
			copyHome := filepath.Join(base, "copy")
			if err := os.MkdirAll(copyHome, 0o755); err != nil {
				t.Fatal(err)
			}
			copyFleetDB(t, home, copyHome)
			dir := filepath.Join(copyHome, "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1"), []string{dir, copyHome}
		}},
		{"a scratch folder in the worktree", func() (string, []string) {
			dir := filepath.Join(wt, "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1"), []string{dir}
		}},
		{"a scratch root symlinked to another folder", func() (string, []string) {
			target := filepath.Join(base, "target")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, "scratch")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(root, "t1-a1"), []string{target}
		}},
		{"a scratch entry symlinked to another folder", func() (string, []string) {
			outside := filepath.Join(base, "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, "scratch")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(root, "t1-a1")
			if err := os.Symlink(outside, entry); err != nil {
				t.Fatal(err)
			}
			return entry, []string{outside}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, watch := tc.make()
			before := map[string]os.FileMode{}
			for _, path := range watch {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = info.Mode().Perm()
			}
			_, entryErr := os.Lstat(dir)
			out, code := execScratch(t, dir)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; output %q", code, out)
			}
			if !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
				t.Fatalf("output = %q", out)
			}
			for _, path := range watch {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != before[path] {
					t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), before[path])
				}
			}
			if entryErr != nil {
				if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the wrapper created %s: %v", dir, err)
				}
			} else if _, err := os.Lstat(dir); err != nil {
				t.Errorf("the wrapper removed %s: %v", dir, err)
			}
		})
	}
}

func TestAttemptExecRefusesAKeyThatNamesNoAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "scratch")
	for _, key := range []string{"t1-a2", "t2-a1", "notakey"} {
		dir := filepath.Join(root, key)
		out, code := execScratch(t, dir)
		if code != 2 || !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
			t.Errorf("%s: exit = %d, output %q; want 2 and the expected shape", key, code, out)
		}
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: the wrapper created %s", key, dir)
		}
	}
}

func TestAttemptExecRefusesARelativeDirectory(t *testing.T) {
	for _, dir := range []string{"scratch/t1-a1", ""} {
		out, code := execScratch(t, dir)
		if code != 2 || !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
			t.Errorf("--dir %q: exit = %d, output %q; want 2 and the expected shape", dir, code, out)
		}
	}
}

func TestAttemptExecPassesStdinAndExitCode(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	scratch := scratchOf(fx.h, fx.h.worktree("t1-a1"))
	eventually(t, func() bool { return exists(scratch) })
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			t.Skip("no ComSpec")
		}
		cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", scratch, "--", comspec, "/c", "exit 7")
		cmd.Env = append(os.Environ(), "HAND_TEST_AS_HAND=1")
		out, err := cmd.CombinedOutput()
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 7 {
			t.Fatalf("exit = %v, want 7; output %q", err, out)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", scratch, "--", "/bin/sh", "-c", "cat; exit 7")
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

func TestAttemptExecWarnsWhenTheScratchPathIsTooLongForAUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sun_path is a Unix limit")
	}
	fx := newAttemptFixtureAt(t, filepath.Join(t.TempDir(), strings.Repeat("long", 25), "fleet"))
	fx.start()
	scratch := filepath.Join(fx.h.home, "scratch", "t1-a1")
	if len(scratch)+8 <= 104 {
		t.Fatalf("the fixture's scratch path is only %d bytes", len(scratch))
	}
	out, code := execScratch(t, scratch)
	if code != 0 {
		t.Fatalf("exit = %d, output %q", code, out)
	}
	if !strings.Contains(out, "AF_UNIX path too long") {
		t.Fatalf("output = %q", out)
	}
}

func TestHiddenExecIsNotInHelpOrAnAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	if _, errOut, code := fx.h.run("attempt"); code != 2 || strings.Contains(errOut, "_exec") {
		t.Fatalf("attempt help: code=%d stderr=%q", code, errOut)
	}
	fx.start()
	out := fx.h.ok("attempt", "list")
	if !strings.Contains(out, "a1,t1,claude") || strings.Contains(out, "_exec") {
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
	if !strings.Contains(out, "scratch_removed[1]{path,bytes}") || !strings.Contains(out, toon.Value(scratch)) {
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
	if !strings.Contains(out, "orphans[1]{path,bytes}") || !strings.Contains(out, toon.Value(orphan)+",10") || strings.Contains(out, toon.Value(live)) {
		t.Fatalf("gc = %q", out)
	}
	out = fx.h.ok("attempt", "gc", "--apply")
	if !strings.Contains(out, "applied: true") || !strings.Contains(out, toon.Value(orphan)) {
		t.Fatalf("gc --apply = %q", out)
	}
	if exists(orphan) || !exists(live) {
		t.Fatalf("after --apply: orphan=%v live=%v", exists(orphan), exists(live))
	}
}

func TestGCSkipsALiveAttemptsScratch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removing a live worktree is Unix-only")
	}
	fx := newAttemptFixture(t)
	fx.start()
	wt := fx.h.worktree("t1-a1")
	scratch := scratchOf(fx.h, wt)
	eventually(t, func() bool { return exists(scratch) })
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if out := fx.h.ok("attempt", "gc", "--apply"); strings.Contains(out, toon.Value(scratch)) {
		t.Fatalf("gc = %q", out)
	}
	if !exists(scratch) {
		t.Fatal("gc removed a live attempt's scratch")
	}
	fx.h.ok("attempt", "stop", "a1")
	if out := fx.h.ok("attempt", "gc"); !strings.Contains(out, toon.Value(scratch)) {
		t.Fatalf("gc after stop = %q", out)
	}
}
