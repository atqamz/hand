//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

var handBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hand-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	handBin = filepath.Join(dir, "hand")
	build := exec.Command("go", "build", "-o", handBin, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build hand: %v: %s\n", err, out)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type invocation struct {
	code   int
	stdout string
	stderr string
}

func runHand(t *testing.T, home string, args ...string) invocation {
	t.Helper()
	cmd := exec.Command(handBin, args...)
	cmd.Dir = home
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run hand %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("$ hand %s\n  exit %d\n  stdout: %s\n  stderr: %s",
		strings.Join(args, " "), code, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if got := runHand(t, home, "init"); got.code != 0 {
		t.Fatalf("hand init: exit %d, stderr %q", got.code, got.stderr)
	}
	return home
}

func registerProject(t *testing.T, home, name, mode string) {
	t.Helper()
	line := fmt.Sprintf("- %s: https://example.com/%s.git mode=%s\n", name, name, mode)
	f, err := os.OpenFile(filepath.Join(home, "data", "projects.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func writeBrief(t *testing.T, home, id string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "data", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", id, "brief.md"), []byte("# brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertInvocation(t *testing.T, got invocation, wantCode int, wantStderr string) {
	t.Helper()
	if got.code != wantCode {
		t.Fatalf("exit = %d, want %d (stderr %q, stdout %q)", got.code, wantCode, got.stderr, got.stdout)
	}
	if wantStderr != "" && !strings.Contains(got.stderr, wantStderr) {
		t.Fatalf("stderr = %q, want it to contain %q", got.stderr, wantStderr)
	}
	if wantCode != 0 && strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("stdout = %q, want errors on stderr only", got.stdout)
	}
}

func TestExitCodeZeroOnSuccess(t *testing.T) {
	home := newHome(t)
	registerProject(t, home, "demo", "direct-pr")

	for _, args := range [][]string{{"--version"}, {"init"}, {"project", "list"}, {"project"}, {"--help"}} {
		got := runHand(t, home, args...)
		if got.code != 0 {
			t.Fatalf("hand %v: exit = %d, want 0 (stderr %q)", args, got.code, got.stderr)
		}
		if strings.TrimSpace(got.stdout) == "" {
			t.Fatalf("hand %v: stdout empty, want structured output on stdout", args)
		}
	}
}

func TestExitCodeTwoOnUsageError(t *testing.T) {
	home := newHome(t)

	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"unknown command", []string{"bogus-command"}, `unknown command "bogus-command"`},
		{"unknown project subcommand", []string{"project", "bogus"}, `unknown command "bogus"`},
		{"unknown completion subcommand", []string{"completion", "bogus"}, `unknown command "bogus"`},
		{"too few args", []string{"spawn", "only-one"}, "accepts 2 arg"},
		{"too many args", []string{"teardown", "task-1", "extra"}, "accepts 1 arg"},
		{"args on argless command", []string{"watch", "extra"}, "unknown command"},
		{"unknown flag", []string{"spawn", "--bogus", "task-1", "demo"}, "unknown flag: --bogus"},
		{"conflicting merge methods", []string{"merge", "task-1", "--squash", "--rebase"}, "only one of --squash, --merge, --rebase"},
		{"merge method with local", []string{"merge", "task-1", "--local", "--squash"}, "cannot be combined with --local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertInvocation(t, runHand(t, home, tc.args...), 2, tc.wantStderr)
		})
	}
}

func TestExitCodeThreeOnPreconditionFailure(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T, home string)
		args       []string
		wantStderr string
	}{
		{
			name:       "task not found",
			args:       []string{"status", "nosuch"},
			wantStderr: `task "nosuch" not found`,
		},
		{
			name:       "teardown unknown task",
			args:       []string{"teardown", "nosuch"},
			wantStderr: `task "nosuch" not found`,
		},
		{
			name:       "merge unknown task",
			args:       []string{"merge", "nosuch"},
			wantStderr: `task "nosuch" not found`,
		},
		{
			name:       "promote unknown task",
			args:       []string{"promote", "nosuch"},
			wantStderr: `task "nosuch" not found`,
		},
		{
			name:       "send to unknown task",
			args:       []string{"send", "nosuch", "hello"},
			wantStderr: `task "nosuch" not found`,
		},
		{
			name:       "spawn on unregistered project",
			args:       []string{"spawn", "task-1", "nosuch"},
			wantStderr: `project "nosuch" not registered`,
		},
		{
			name:       "project sync unregistered",
			args:       []string{"project", "sync", "nosuch"},
			wantStderr: `project "nosuch" not registered`,
		},
		{
			name:       "project remove unregistered",
			args:       []string{"project", "remove", "nosuch"},
			wantStderr: `project "nosuch" not registered`,
		},
		{
			name:       "spawn without brief",
			setup:      func(t *testing.T, home string) { registerProject(t, home, "demo", "direct-pr") },
			args:       []string{"spawn", "task-1", "demo"},
			wantStderr: "brief not found at data/task-1/brief.md",
		},
		{
			name: "project add already registered",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
			},
			args:       []string{"project", "add", "https://example.com/demo.git"},
			wantStderr: `project "demo" already registered`,
		},
		{
			name: "project remove with active task",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
				if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip}); err != nil {
					t.Fatal(err)
				}
			},
			args:       []string{"project", "remove", "demo"},
			wantStderr: `project "demo" has active tasks referencing it`,
		},
		{
			name: "teardown scout without report",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
				if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout}); err != nil {
					t.Fatal(err)
				}
			},
			args:       []string{"teardown", "task-1"},
			wantStderr: "report not found at data/task-1/report.md",
		},
		{
			name: "teardown ship without landed work",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
				worktree := filepath.Join(home, "wt")
				initGitRepo(t, worktree)
				if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Worktree: worktree}); err != nil {
					t.Fatal(err)
				}
			},
			args:       []string{"teardown", "task-1"},
			wantStderr: "no PR recorded for task-1",
		},
		{
			name: "promote a task that is not a scout",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
				if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip}); err != nil {
					t.Fatal(err)
				}
			},
			args:       []string{"promote", "task-1"},
			wantStderr: `task "task-1" is not a scout`,
		},
		{
			name: "merge an already merged task",
			setup: func(t *testing.T, home string) {
				registerProject(t, home, "demo", "direct-pr")
				if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindShip, Merged: true}); err != nil {
					t.Fatal(err)
				}
			},
			args:       []string{"merge", "task-1"},
			wantStderr: "task task-1 already merged",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := newHome(t)
			if tc.setup != nil {
				tc.setup(t, home)
			}
			assertInvocation(t, runHand(t, home, tc.args...), 3, tc.wantStderr)
		})
	}
}

func TestExitCodeOneOnGeneralError(t *testing.T) {
	home := newHome(t)

	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"invalid project URL", []string{"project", "add", "not-a-url"}, "invalid project URL"},
		{"invalid project mode", []string{"project", "add", "https://example.com/demo.git", "--mode", "bogus"}, "invalid project mode"},
		{"invalid poll interval", []string{"watch", "--poll", "nonsense"}, "invalid poll interval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertInvocation(t, runHand(t, home, tc.args...), 1, tc.wantStderr)
		})
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "initial commit")
}
