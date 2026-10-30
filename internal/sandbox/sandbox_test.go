package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(home string) func(string) string {
	return func(k string) string {
		switch k {
		case "HOME":
			return home
		case "PATH":
			return "/usr/bin:/bin"
		}
		return ""
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestBoxedGrantsTheWorktreeAndNotTheHomeSecrets(t *testing.T) {
	home := realPath(t, t.TempDir())
	wt := filepath.Join(home, "worktrees", "fleet", "t1-a2")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(Request{
		Mode:     Boxed,
		Worktree: wt,
		Home:     home,
		Getenv:   testEnv(home),
		Environ:  []string{"PATH=/usr/bin", "LUVUS_HOME=/run/luvus", "SSH_AUTH_SOCK=/run/agent", "GH_TOKEN=t", "HAND_LUVUS_BASE=https://example.test/luvus", "KEEP=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var worktree, temp bool
	for _, rule := range p.Rules {
		if rule.Path == wt && rule.Write {
			worktree = true
		}
		if rule.Path == p.Temp && rule.Write {
			temp = true
		}
		if strings.HasPrefix(rule.Path, ssh) {
			t.Fatalf("the policy grants %s", rule.Path)
		}
		if rule.Path == filepath.Join(home, ".config", "gh") {
			t.Fatalf("the policy grants the gh login")
		}
	}
	if !worktree {
		t.Fatalf("the policy does not grant the worktree: %v", p.Rules)
	}
	if !temp {
		t.Fatalf("the policy does not grant its temporary folder")
	}
	if fi, err := os.Stat(p.Temp); err != nil || !fi.IsDir() {
		t.Fatalf("temp %s: %v", p.Temp, err)
	}
	if got := envValue(p.Environ, "TMPDIR"); got != p.Temp {
		t.Fatalf("TMPDIR = %q, want %q", got, p.Temp)
	}
	for _, name := range []string{"LUVUS_HOME", "HAND_LUVUS_BASE", "SSH_AUTH_SOCK", "GH_TOKEN"} {
		if envValue(p.Environ, name) != "" {
			t.Fatalf("%s survived the rebuild", name)
		}
	}
	if envValue(p.Environ, "KEEP") != "1" {
		t.Fatalf("the rebuild dropped KEEP")
	}
	if strings.Join(p.Dropped, ",") != "GH_TOKEN,HAND_LUVUS_BASE,LUVUS_HOME,SSH_AUTH_SOCK" {
		t.Fatalf("dropped = %v", p.Dropped)
	}
}

func TestBoxedGrantsTheGitFolderInParts(t *testing.T) {
	home := realPath(t, t.TempDir())
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(home, "repo", ".git")
	for _, dir := range []string{"objects", "refs", "logs", "hooks", "info"} {
		if err := os.MkdirAll(filepath.Join(common, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	private := filepath.Join(common, "worktrees", "t1-a2")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(home, "remote.git")
	for _, dir := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(remote, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Resolve(Request{
		Mode:       Boxed,
		Worktree:   wt,
		GitCommon:  common,
		GitPrivate: private,
		Remotes:    []string{remote},
		Home:       home,
		Getenv:     testEnv(home),
		Environ:    []string{"PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rule := range p.Rules {
		got[rule.Path] = rule.Write
	}
	for _, dir := range []string{"objects", "refs", "logs"} {
		if write, ok := got[filepath.Join(common, dir)]; !ok || !write {
			t.Fatalf("%s is not granted for writing", dir)
		}
	}
	for _, dir := range []string{"hooks", "info"} {
		if write, ok := got[filepath.Join(common, dir)]; ok && write {
			t.Fatalf("%s is granted for writing", dir)
		}
	}
	if write, ok := got[common]; !ok || write {
		t.Fatalf("the git folder itself is not granted read-only")
	}
	if write, ok := got[private]; !ok || !write {
		t.Fatalf("the linked worktree's git folder is not granted for writing")
	}
	if write, ok := got[remote]; !ok || write {
		t.Fatalf("the local remote is not granted read-only")
	}
	for _, dir := range []string{"objects", "refs"} {
		if write, ok := got[filepath.Join(remote, dir)]; !ok || !write {
			t.Fatalf("the remote's %s is not granted for writing", dir)
		}
	}
	if write, ok := got[filepath.Join(remote, "hooks")]; ok && write {
		t.Fatalf("the remote's hooks are granted for writing")
	}
}

func TestOpenKeepsTheEnvironment(t *testing.T) {
	p, err := Resolve(Request{Mode: Open, Environ: []string{"LUVUS_HOME=/run", "TMPDIR=/tmp"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 0 {
		t.Fatalf("open resolved rules: %v", p.Rules)
	}
	if strings.Join(p.Environ, ",") != "LUVUS_HOME=/run,TMPDIR=/tmp" {
		t.Fatalf("open changed the environment: %v", p.Environ)
	}
}

func TestResolveRejectsAnUnknownMode(t *testing.T) {
	if _, err := Resolve(Request{Mode: "jailed"}); err == nil {
		t.Fatal("jailed mode was accepted")
	}
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

func TestBoxedKeepsToolchainBinariesReadOnly(t *testing.T) {
	home := realPath(t, t.TempDir())
	data := filepath.Join(home, ".local", "share")
	dirs := map[string]bool{
		filepath.Join(home, "go", "pkg"):                true,
		filepath.Join(home, "go", "bin"):                false,
		filepath.Join(home, ".cargo", "registry"):       true,
		filepath.Join(home, ".cargo", "bin"):            false,
		filepath.Join(home, ".bun", "install", "cache"): true,
		filepath.Join(home, ".bun", "bin"):              false,
		filepath.Join(home, ".rustup", "downloads"):     true,
		filepath.Join(home, ".rustup", "toolchains"):    false,
		filepath.Join(data, "mise"):                     false,
		filepath.Join(data, "mise", "installs"):         false,
		filepath.Join(data, "mise", "shims"):            false,
		filepath.Join(home, ".local", "bin"):            false,
	}
	for dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(Request{
		Mode:     Boxed,
		Worktree: wt,
		Home:     home,
		Getenv:   testEnv(home),
		Environ:  []string{"PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rule := range p.Rules {
		got[rule.Path] = rule.Write
	}
	for dir, write := range dirs {
		if got[dir] != write {
			t.Fatalf("%s write = %v, want %v", dir, got[dir], write)
		}
	}
}

func TestBoxedNamesItsCaches(t *testing.T) {
	home := realPath(t, t.TempDir())
	cache := filepath.Join(home, ".cache")
	stateDir := filepath.Join(home, ".local", "state")
	for _, dir := range []string{filepath.Join(cache, "mise"), filepath.Join(cache, "go-build"), filepath.Join(cache, "other"), filepath.Join(stateDir, "mise"), filepath.Join(stateDir, "mise", "trusted-configs"), filepath.Join(stateDir, "mise", "tracked-configs"), filepath.Join(stateDir, "other")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: home, Getenv: testEnv(home), Environ: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rule := range p.Rules {
		got[rule.Path] = rule.Write
	}
	for _, dir := range []string{filepath.Join(cache, "mise"), filepath.Join(cache, "go-build"), filepath.Join(stateDir, "mise", "hints"), filepath.Join(stateDir, "mise", "incomplete-installs")} {
		if write, ok := got[dir]; !ok || !write {
			t.Fatalf("%s is not granted for writing", dir)
		}
	}
	for _, dir := range []string{cache, stateDir, filepath.Join(cache, "other"), filepath.Join(stateDir, "other")} {
		if write, ok := got[dir]; ok && write {
			t.Fatalf("%s is granted for writing", dir)
		}
	}
	for _, dir := range []string{filepath.Join(stateDir, "mise"), filepath.Join(stateDir, "mise", "trusted-configs"), filepath.Join(stateDir, "mise", "tracked-configs")} {
		if write, ok := got[dir]; ok && write {
			t.Fatalf("%s is granted for writing", dir)
		}
	}
}

func TestBoxedGrantsOpencodeFoldersOnlyToOpencode(t *testing.T) {
	home := realPath(t, t.TempDir())
	data := filepath.Join(home, ".local", "share", "opencode")
	state := filepath.Join(home, ".local", "state", "opencode")
	for _, dir := range []string{data, state} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	for harness, want := range map[string]bool{"claude": false, "opencode": true} {
		p, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: home, Harness: harness, Getenv: testEnv(home), Environ: []string{"PATH=/usr/bin"}})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, rule := range p.Rules {
			got[rule.Path] = rule.Write
		}
		for _, dir := range []string{data, state} {
			if write, ok := got[dir]; ok != want || (ok && write != want) {
				t.Fatalf("%s: %s granted = %v, want %v", harness, dir, ok, want)
			}
		}
	}
}

func TestBoxedCreatesHarnessFoldersBeforeGrantingThem(t *testing.T) {
	home := realPath(t, t.TempDir())
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "opencode")
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state folder exists before the policy: %v", err)
	}
	p, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: home, Harness: "opencode", Getenv: testEnv(home), Environ: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rule := range p.Rules {
		got[rule.Path] = rule.Write
	}
	if write, ok := got[state]; !ok || !write {
		t.Fatalf("%s granted = %v write = %v, want write", state, ok, write)
	}
	if fi, err := os.Stat(state); err != nil || !fi.IsDir() {
		t.Fatalf("state folder after the policy = %v, %v", fi, err)
	}
}

func TestBoxedNarrowsToolFolders(t *testing.T) {
	home := realPath(t, t.TempDir())
	data := filepath.Join(home, ".local", "share")
	bun := filepath.Join(home, ".bun", "install")
	credentials := filepath.Join(home, ".cargo", "credentials.toml")
	config := filepath.Join(home, ".cargo", "config.toml")
	registry := filepath.Join(home, ".cargo", "registry")
	for _, dir := range []string{filepath.Join(data, "pnpm"), bun, registry} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{credentials: "token = secret\n", config: "[build]\ntarget-dir = \"/tmp\"\n"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: home, Getenv: testEnv(home), Environ: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rule := range p.Rules {
		got[rule.Path] = rule.Write
	}
	for _, dir := range []string{filepath.Join(data, "pnpm", "store"), filepath.Join(bun, "cache"), registry} {
		if write, ok := got[dir]; !ok || !write {
			t.Fatalf("%s granted = %v write = %v, want write", dir, ok, write)
		}
	}
	for _, dir := range []string{filepath.Join(data, "pnpm"), bun, filepath.Join(home, ".cargo")} {
		if write, ok := got[dir]; ok && write {
			t.Fatalf("%s is granted for writing", dir)
		}
	}
	for _, path := range []string{filepath.Join(data, "pnpm"), config} {
		if write, ok := got[path]; !ok || write {
			t.Fatalf("%s granted = %v write = %v, want read-only", path, ok, write)
		}
	}
	for path := range got {
		if covers(path, credentials) {
			t.Fatalf("%s covers %s", path, credentials)
		}
	}
}

func covers(rule, path string) bool {
	return rule == path || strings.HasPrefix(path, rule+string(filepath.Separator))
}

func TestBoxedGrantsNoHomeRulesWithoutHOME(t *testing.T) {
	root := realPath(t, t.TempDir())
	wt := filepath.Join(root, "wt")
	cwd := filepath.Join(root, "cwd")
	for _, dir := range []string{wt, cwd} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	empty := func(string) string { return "" }
	if _, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: filepath.Join(root, "fleet"), Harness: "opencode", Getenv: empty, Environ: []string{"PATH=/usr/bin"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("resolving without HOME created %v in the working directory", names)
	}
	if _, err := os.Stat(filepath.Join(root, "fleet", "tmp")); err != nil {
		t.Fatalf("private temp = %v", err)
	}
}

func TestBoxedLeavesTheFleetHomeAlone(t *testing.T) {
	root := realPath(t, t.TempDir())
	home := filepath.Join(root, "home")
	fleet := filepath.Join(root, "fleet")
	wt := filepath.Join(fleet, "worktrees", "fleet", "t1-a2")
	for _, dir := range []string{wt, filepath.Join(fleet, "memory"), filepath.Join(home, ".cache")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(fleet, "hand.db"), filepath.Join(fleet, "hand.db-wal"), filepath.Join(fleet, "hand.db-shm"), filepath.Join(fleet, "hand.db.lock"), filepath.Join(fleet, "board.token"), filepath.Join(fleet, "memory", "notes.md")} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Resolve(Request{Mode: Boxed, Worktree: wt, Home: fleet, Getenv: testEnv(home), Environ: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range p.Rules {
		if rule.Path == fleet || strings.HasPrefix(rule.Path, fleet+string(filepath.Separator)) {
			if rule.Path == p.Temp || rule.Path == wt {
				continue
			}
			t.Fatalf("the policy grants %s", rule.Path)
		}
	}
}
