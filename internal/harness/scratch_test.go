package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

const fleetID = "f0123456789ab"

func scratchEnv(tmp string) func(string) string {
	return func(k string) string {
		if k == "TMPDIR" {
			return tmp
		}
		return ""
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func claudeDir(tmp string) string {
	return filepath.Join(tmp, "claude-"+strconv.Itoa(os.Getuid()))
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the scratch cleaner is Unix-only")
	}
}

func TestClaudeKeyReplacesEveryNonAlphanumeric(t *testing.T) {
	got := claudeKey("/home/atqa/.secondhand/worktrees/f0123456789ab/t308-a343")
	if want := "-home-atqa--secondhand-worktrees-f0123456789ab-t308-a343"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestLeftoversNamesOnlyTheExactScratchDirectory(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "home", ".secondhand", "worktrees", fleetID, "t7-a3")
	want := filepath.Join(claudeDir(tmp), claudeKey(wt))
	mkdir(t, want)
	mkdir(t, filepath.Join(claudeDir(tmp), claudeKey(wt)+"-1"))
	if got := Leftovers("claude", wt, uid, scratchEnv(tmp)); !slices.Equal(got, []string{want}) {
		t.Fatalf("Leftovers = %v, want %v", got, []string{want})
	}
	if got := Leftovers("claude", wt, uid+1, scratchEnv(tmp)); got != nil {
		t.Fatalf("another uid = %v", got)
	}
}

func TestLeftoversRefusesAnythingButAnAttemptWorktree(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	base := filepath.Join(tmp, "home", ".secondhand", "worktrees")
	for _, wt := range []string{
		filepath.Join(base, fleetID),
		filepath.Join(base, fleetID, "t7-a3", "sub"),
		filepath.Join(base, "fleet", "t7-a3"),
		filepath.Join(base, fleetID, "t7"),
		filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home", ".secondhand", "elsewhere", fleetID, "t7-a3"),
		"worktrees/" + fleetID + "/t7-a3",
	} {
		mkdir(t, filepath.Join(claudeDir(tmp), claudeKey(wt)))
		if got := Leftovers("claude", wt, uid, scratchEnv(tmp)); got != nil {
			t.Errorf("Leftovers(%q) = %v", wt, got)
		}
	}
	wt := filepath.Join(base, fleetID, "t7-a3")
	mkdir(t, filepath.Join(claudeDir(tmp), claudeKey(wt)))
	for _, name := range []string{"codex", "opencode", "agy", "copilot"} {
		if got := Leftovers(name, wt, uid, scratchEnv(tmp)); got != nil {
			t.Errorf("Leftovers(%s) = %v", name, got)
		}
	}
	if got := Leftovers("claude", wt, -1, scratchEnv(tmp)); got != nil {
		t.Errorf("no uid = %v", got)
	}
}

func TestLeftoversSkipsASymlinkAndAMissingDirectory(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	wt := filepath.Join(tmp, "worktrees", fleetID, "t7-a3")
	if got := Leftovers("claude", wt, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("missing = %v", got)
	}
	mkdir(t, claudeDir(tmp))
	if err := os.Symlink(t.TempDir(), filepath.Join(claudeDir(tmp), claudeKey(wt))); err != nil {
		t.Fatal(err)
	}
	if got := Leftovers("claude", wt, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("symlink = %v", got)
	}
}

func TestOrphansListsOnlyKeysWithoutAWorktreeFolder(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	worktrees := filepath.Join(tmp, "home", ".secondhand", "worktrees")
	mkdir(t, filepath.Join(worktrees, fleetID, "t1-a1"))
	root := claudeDir(tmp)
	live := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t1-a1")))
	orphan := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t2-a2")))
	foreign := filepath.Join(root, claudeKey(filepath.Join(tmp, "other", "worktrees", fleetID, "t2-a2")))
	odd := filepath.Join(root, "-home-work")
	for _, d := range []string{live, orphan, foreign, odd} {
		mkdir(t, d)
	}
	file := filepath.Join(root, claudeKey(filepath.Join(worktrees, fleetID, "t3-a3")))
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Orphans("claude", worktrees, uid, scratchEnv(tmp)); !slices.Equal(got, []string{orphan}) {
		t.Fatalf("Orphans = %v, want %v", got, []string{orphan})
	}
	if got := Orphans("codex", worktrees, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("codex = %v", got)
	}
}

func TestScratchRootsByPlatform(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	cases := []struct {
		goos string
		vars map[string]string
		want []string
	}{
		{"linux", nil, []string{"/tmp/claude-7"}},
		{"linux", map[string]string{"TMPDIR": "/run/t/"}, []string{"/run/t/claude-7"}},
		{"linux", map[string]string{"TMPDIR": "/run/t", "CLAUDE_CODE_TMPDIR": "/c"}, []string{"/c/claude-7"}},
		{"darwin", nil, []string{"/tmp/claude-7"}},
		{"darwin", map[string]string{"TMPDIR": "/var/folders/x/T/"}, []string{"/tmp/claude-7", "/var/folders/x/T/claude-7"}},
		{"darwin", map[string]string{"TMPDIR": "/var/folders/x/T", "CLAUDE_CODE_TMPDIR": "/c"}, []string{"/c/claude-7", "/var/folders/x/T/claude-7"}},
		{"darwin", map[string]string{"TMPDIR": "/tmp/"}, []string{"/tmp/claude-7"}},
	}
	for _, c := range cases {
		want := make([]string, len(c.want))
		for i, w := range c.want {
			want[i] = filepath.FromSlash(w)
		}
		if got := claudeScratchRoots(c.goos, env(c.vars), 7); !slices.Equal(got, want) {
			t.Errorf("%s %v = %v, want %v", c.goos, c.vars, got, want)
		}
	}
	if got := claudeScratchRoots("linux", env(nil), -1); got != nil {
		t.Errorf("no uid = %v", got)
	}
}

func TestSymlinkedHomeFindsTheScratchKeyedByTheRealPath(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	realWorktrees := filepath.Join(tmp, "data", "hand", "worktrees")
	mkdir(t, filepath.Join(realWorktrees, fleetID))
	mkdir(t, filepath.Join(tmp, "home"))
	if err := os.Symlink(filepath.Join(tmp, "data", "hand"), filepath.Join(tmp, "home", ".secondhand")); err != nil {
		t.Fatal(err)
	}
	worktrees := filepath.Join(tmp, "home", ".secondhand", "worktrees")
	scratch := filepath.Join(claudeDir(tmp), claudeKey(filepath.Join(realWorktrees, fleetID, "t7-a3")))
	mkdir(t, scratch)
	if got := Leftovers("claude", filepath.Join(worktrees, fleetID, "t7-a3"), uid, scratchEnv(tmp)); !slices.Equal(got, []string{scratch}) {
		t.Fatalf("Leftovers = %v, want %v", got, []string{scratch})
	}
	if got := Orphans("claude", worktrees, uid, scratchEnv(tmp)); !slices.Equal(got, []string{scratch}) {
		t.Fatalf("Orphans = %v, want %v", got, []string{scratch})
	}
	mkdir(t, filepath.Join(realWorktrees, fleetID, "t7-a3"))
	if got := Orphans("claude", worktrees, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("live worktree listed: %v", got)
	}
}

func TestASymlinkedOrForeignClaudeRootIsRefused(t *testing.T) {
	skipWindows(t)
	uid := os.Getuid()
	tmp := t.TempDir()
	worktrees := filepath.Join(tmp, "worktrees")
	wt := filepath.Join(worktrees, fleetID, "t7-a3")
	elsewhere := filepath.Join(tmp, "elsewhere")
	mkdir(t, filepath.Join(elsewhere, claudeKey(wt)))
	if err := os.Symlink(elsewhere, claudeDir(tmp)); err != nil {
		t.Fatal(err)
	}
	if got := Leftovers("claude", wt, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("Leftovers = %v", got)
	}
	if got := Orphans("claude", worktrees, uid, scratchEnv(tmp)); got != nil {
		t.Fatalf("Orphans = %v", got)
	}
}
