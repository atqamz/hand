package cli_test

import (
	"crypto/sha256"
	"database/sql"
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

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	_ "modernc.org/sqlite"
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
	root := t.TempDir()
	dir := filepath.Join(root, "gone", "scratch", "t1-a1")
	before := snapshotTree(t, root)
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
	assertTreeUnchanged(t, before, root)
}

func execScratch(t *testing.T, h *harness, dir string, command ...string) (string, int) {
	t.Helper()
	if len(command) == 0 {
		command = []string{"/usr/bin/true"}
	}
	args := append([]string{os.Args[0], "attempt", "_exec", "--dir", dir, "--"}, command...)
	cmd := exec.Command(args[0], args[1:]...)
	overrides := []string{"HAND_TEST_AS_HAND=1"}
	if h != nil {
		overrides = append(overrides, "SECONDHAND_HOME="+h.vars["SECONDHAND_HOME"])
	}
	cmd.Env = environment(os.Environ(), overrides...)
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
}

type treeFile struct {
	mode fs.FileMode
	sum  string
	link string
}

func snapshotTree(t *testing.T, root string) map[string]treeFile {
	t.Helper()
	files := map[string]treeFile{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := treeFile{mode: info.Mode()}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			entry.link, err = os.Readlink(path)
		case info.Mode().IsRegular():
			var b []byte
			b, err = os.ReadFile(path)
			sum := sha256.Sum256(b)
			entry.sum = string(sum[:])
		}
		if err != nil {
			return err
		}
		files[rel] = entry
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return files
}

func assertTreeUnchanged(t *testing.T, before map[string]treeFile, root string) {
	t.Helper()
	after := snapshotTree(t, root)
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Errorf("%s disappeared from %s", rel, root)
			continue
		}
		if got != want {
			t.Errorf("%s changed in %s: mode %v -> %v, bytes %q -> %q, link %q -> %q", rel, root, want.mode, got.mode, want.sum, got.sum, want.link, got.link)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("%s appeared in %s", rel, root)
		}
	}
}

func fleetLink(t *testing.T, h *harness) string {
	t.Helper()
	dir := filepath.Join(h.vars["SECONDHAND_HOME"], "fleets")
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		if state.FleetID.MatchString(de.Name()) {
			names = append(names, de.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("fleets under %s = %v, want one", dir, names)
	}
	return filepath.Join(dir, names[0])
}

func awaitScratch(t *testing.T, home string) {
	t.Helper()
	eventually(t, func() bool { return exists(filepath.Join(home, "scratch", "t1-a1")) })
}

func TestAttemptExecRefusesEveryPathButTheAttemptsScratch(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	awaitScratch(t, home)
	wt := fx.h.worktree("t1-a1")
	base := t.TempDir()
	cases := []struct {
		name string
		make func() string
	}{
		{"an unrelated scratch folder", func() string {
			dir := filepath.Join(base, "team", "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1")
		}},
		{"a folder whose parent is not named scratch", func() string {
			dir := filepath.Join(base, "work")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1")
		}},
		{"a deeper scratch folder in the fleet home", func() string {
			dir := filepath.Join(home, "sub", "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1")
		}},
		{"another fleet home with a copy of the database", func() string {
			copyHome := filepath.Join(base, "copy")
			if err := os.MkdirAll(copyHome, 0o755); err != nil {
				t.Fatal(err)
			}
			copyFleetDB(t, home, copyHome)
			dir := filepath.Join(copyHome, "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1")
		}},
		{"an unrelated folder with its own hand.db", func() string {
			dir := filepath.Join(base, "notes")
			if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(dir, "hand.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE notes(body TEXT)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO notes(body) VALUES ('keep me')`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "scratch", "t1-a1")
		}},
		{"a hand.db that is not a database", func() string {
			dir := filepath.Join(base, "notdb")
			if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hand.db"), []byte("not a database\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "scratch", "t1-a1")
		}},
		{"a hardlink to the fleet database", func() string {
			dir := filepath.Join(base, "hlink")
			if err := os.MkdirAll(filepath.Join(dir, "scratch"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(filepath.Join(home, "hand.db"), filepath.Join(dir, "hand.db")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "scratch", "t1-a1")
		}},
		{"a scratch folder in the worktree", func() string {
			dir := filepath.Join(wt, "scratch")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "t1-a1")
		}},
		{"a scratch root symlinked to another folder", func() string {
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
			return filepath.Join(root, "t1-a1")
		}},
		{"a scratch entry symlinked to another folder", func() string {
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
			return entry
		}},
		{"a scratch entry that is a dangling symlink", func() string {
			root := filepath.Join(home, "scratch")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(root, "t1-a1")
			if err := os.Symlink(filepath.Join(base, "nowhere"), entry); err != nil {
				t.Fatal(err)
			}
			return entry
		}},
		{"a scratch root that is a dangling symlink", func() string {
			root := filepath.Join(home, "scratch")
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(base, "nowhere"), root); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(root, "t1-a1")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.make()
			beforeBase := snapshotTree(t, base)
			beforeHome := snapshotTree(t, home)
			out, code := execScratch(t, fx.h, dir)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; output %q", code, out)
			}
			if !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
				t.Fatalf("output = %q", out)
			}
			assertTreeUnchanged(t, beforeBase, base)
			assertTreeUnchanged(t, beforeHome, home)
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
	awaitScratch(t, home)
	root := filepath.Join(home, "scratch")
	for _, key := range []string{"t1-a2", "t2-a1", "notakey"} {
		dir := filepath.Join(root, key)
		before := snapshotTree(t, home)
		out, code := execScratch(t, fx.h, dir)
		if code != 2 || !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
			t.Errorf("%s: exit = %d, output %q; want 2 and the expected shape", key, code, out)
		}
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: the wrapper created %s", key, dir)
		}
		assertTreeUnchanged(t, before, home)
	}
}

func TestAttemptExecRefusesAHomeTheSharedFolderDoesNotRegister(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	awaitScratch(t, home)
	scratch := filepath.Join(home, "scratch", "t1-a1")
	link := fleetLink(t, fx.h)
	original, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"a link that points at another folder", func() error {
			return os.WriteFile(link, []byte(elsewhere+"\n"), 0o600)
		}},
		{"a link that points at a missing folder", func() error {
			return os.WriteFile(link, []byte(filepath.Join(t.TempDir(), "gone")+"\n"), 0o600)
		}},
		{"no registration at all", func() error {
			return os.Remove(link)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(link, original, 0o600); err != nil {
					t.Fatalf("restore %s: %v", link, err)
				}
			}()
			before := snapshotTree(t, home)
			out, code := execScratch(t, fx.h, scratch)
			if code != 2 || !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
				t.Fatalf("exit = %d, output %q; want 2 and the expected shape", code, out)
			}
			assertTreeUnchanged(t, before, home)
		})
	}
}

func TestAttemptExecRefusesARegisteredHomeWithoutItsDatabase(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	home, err := filepath.EvalSymlinks(fx.h.home)
	if err != nil {
		t.Fatal(err)
	}
	awaitScratch(t, home)
	scratch := filepath.Join(home, "scratch", "t1-a1")
	for _, name := range []string{"hand.db", "hand.db-wal", "hand.db-shm", "hand.db.lock"} {
		if err := os.Remove(filepath.Join(home, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	before := snapshotTree(t, home)
	out, code := execScratch(t, fx.h, scratch)
	if code != 2 || !strings.Contains(out, "<fleet home>/scratch/tN-aM") {
		t.Fatalf("exit = %d, output %q; want 2 and the expected shape", code, out)
	}
	assertTreeUnchanged(t, before, home)
}

func TestAttemptExecRefusesARelativeDirectory(t *testing.T) {
	for _, dir := range []string{"scratch/t1-a1", ""} {
		out, code := execScratch(t, nil, dir)
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
	overrides := []string{"HAND_TEST_AS_HAND=1", "SECONDHAND_HOME=" + fx.h.vars["SECONDHAND_HOME"]}
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			t.Skip("no ComSpec")
		}
		cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", scratch, "--", comspec, "/c", "exit 7")
		cmd.Env = environment(os.Environ(), overrides...)
		out, err := cmd.CombinedOutput()
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 7 {
			t.Fatalf("exit = %v, want 7; output %q", err, out)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "attempt", "_exec", "--dir", scratch, "--", "/bin/sh", "-c", "cat; exit 7")
	cmd.Env = environment(os.Environ(), overrides...)
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
	out, code := execScratch(t, fx.h, scratch)
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
	before := fx.h.ok("attempt", "list")
	if !strings.Contains(before, "a1,t1,claude") || strings.Contains(before, "_exec") {
		t.Fatalf("attempt list = %q", before)
	}
	scratch := scratchOf(fx.h, fx.h.worktree("t1-a1"))
	eventually(t, func() bool { return exists(scratch) })
	if out, code := execScratch(t, fx.h, scratch); code != 0 {
		t.Fatalf("_exec: exit = %d, output %q", code, out)
	}
	if after := fx.h.ok("attempt", "list"); after != before {
		t.Fatalf("attempt list after _exec = %q, before %q; _exec changed the attempt table", after, before)
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
