// Package faketool installs stateful fake external CLIs on a test's PATH.
// One fake per tool, shared by every suite, each tracking the state its own
// commands change. FIDELITY.md in this directory records what the real tools do.
package faketool

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// Replaces PATH with an empty directory for the rest of the test, so no tool at all - fake or real
// - can be found. It is how a suite exercises an external executable that is not installed.
func NoTools(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

type commandConfig struct {
	Name       string
	Args       bool
	Stdout     string
	Stderr     string
	Exit       int
	Log        string
	FileAction *FileAction
}

// Command installs a narrow fixed-result process for tests that only need to
// inspect argv or exercise stdout, stderr and exit-code handling.
type Command struct {
	Name       string
	Args       bool
	Stdout     string
	Stderr     string
	Exit       int
	Log        string
	FileAction *FileAction
}

// FileAction is the one filesystem operation supported by Command.
type FileAction struct {
	PathArg  int
	Relative string
	Content  string
	Append   bool
}

func (c Command) Install(t *testing.T, bin string) {
	t.Helper()
	installConfig(t, bin, c.Name, "command", commandConfig(c))
}

type installSpec struct {
	Kind    string
	Payload json.RawMessage
	Log     string
}

var helperBuild struct {
	sync.Once
	path string
	err  error
}

func installConfig(t *testing.T, bin, name, kind string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s fake config: %v", name, err)
	}
	installConfigData(t, bin, name, installSpec{Kind: kind, Payload: data})
}

func installConfigData(t *testing.T, bin, name string, spec installSpec) {
	t.Helper()
	helper := helperBinary(t)
	target := filepath.Join(bin, executableName(name))
	data, err := os.ReadFile(helper)
	if err != nil {
		t.Fatalf("read faketool helper: %v", err)
	}
	if err := os.WriteFile(target+".tmp", data, 0o755); err != nil {
		t.Fatalf("install fake %s: %v", name, err)
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			t.Fatalf("replace fake %s: %v", name, err)
		}
	}
	if err := os.Rename(target+".tmp", target); err != nil {
		t.Fatalf("install fake %s: %v", name, err)
	}
	config, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal %s install config: %v", name, err)
	}
	writeFile(t, configPath(bin, name), string(config))
}

func helperBinary(t *testing.T) string {
	t.Helper()
	helperBuild.Do(func() {
		root := moduleRoot()
		dir, err := os.MkdirTemp("", "hand-faketool-helper-")
		if err != nil {
			helperBuild.err = fmt.Errorf("create fake helper directory: %w", err)
			return
		}
		target := filepath.Join(dir, executableName("faketool"))
		cmd := exec.Command(goToolPath(), "build", "-o", target, "./internal/faketool/cmd/faketool")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			helperBuild.err = fmt.Errorf("build fake helper: %w: %s", err, output)
			return
		}
		helperBuild.path = target
	})
	if helperBuild.err != nil {
		t.Fatal(helperBuild.err)
	}
	return helperBuild.path
}

func goToolPath() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	//nolint:staticcheck // hermetic test PATHs can hide go, so the running toolchain is the reliable fallback.
	path := filepath.Join(runtime.GOROOT(), "bin", name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

func moduleRoot() string {
	_, source, _, _ := runtime.Caller(0)
	dir := filepath.Dir(source)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func configPath(bin, name string) string {
	return filepath.Join(bin, "."+name+"-config.json")
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

// Encodes an identifier as a filename, so a fake's state can be one file per
// entity. Only the separators matter: herdr tab ids carry a colon and treehouse slots are absolute paths.
func key(id string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "-", ".", "_", " ", "_").Replace(strings.Trim(id, "/\\"))
}
