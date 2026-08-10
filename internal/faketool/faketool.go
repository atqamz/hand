// Package faketool installs stateful fake external CLIs on a test's PATH.
// One fake per tool, shared by every suite, each tracking the state its own
// commands change. FIDELITY.md in this directory records what the real tools do.
package faketool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A real git repo at path, which is what lets the pool fake tell a clean slot
// from a dirty one the way treehouse does. Its commits run on a scratch global
// config, so the operator's commit.gpgsign never drags gpg-agent into a test.
func InitRepo(t *testing.T, path string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = faketool\n\temail = faketool@example.invalid\n" +
		"[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		c := exec.Command("git", args...)
		c.Dir = path
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// Returns a directory prepended to PATH for the rest of the test, so fakes
// installed there are found ahead of any real tool. Prepending rather than
// replacing keeps it additive: several tools' fakes can share one directory.
func Bin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// Dispatches body on selector, the shell expression each case arm matches ("$1",
// or "$1 $2" for a two-word command). The loud default arm is what keeps an
// invocation shape the fake does not model from passing as a silent success.
func install(t *testing.T, bin, name, log, prelude, selector, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake " + name + " is a POSIX shell script, not supported on windows")
	}
	script := "#!/bin/sh\n" + prelude
	if log != "" {
		script += fmt.Sprintf("echo \"%s $@\" >> %s\n", name, quote(log))
	}
	script += fmt.Sprintf("case \"%s\" in\n%s\n  *) echo \"unexpected %s invocation: $@\" >&2; exit 1 ;;\nesac\n",
		selector, body, name)
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Kept beside the fake rather than in a fresh temp dir, so a test that reinstalls
// one - a subtest pointing it at its own slots or panes - resumes the state the
// earlier install left behind instead of silently starting over.
func stateDir(t *testing.T, bin, name string) string {
	t.Helper()
	dir := filepath.Join(bin, "."+name+"-state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Atomic because a test can rewrite a state file while a polling command reads
// it, and a truncating in-place write would let that read see a phantom empty
// value.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path+".tmp", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Encodes an identifier as a filename, so a fake's state can be one file per
// entity and every existence test is a shell builtin. Only the separators matter:
// herdr tab ids carry a colon and treehouse slots are absolute paths.
func key(id string) string {
	return strings.NewReplacer("/", "_", ":", "-", ".", "_", " ", "_").Replace(strings.Trim(id, "/"))
}
