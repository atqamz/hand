package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

func TestValidateProjectName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\\name`, "foo:bar", "repo ", "repo=one"} {
		err := validateProjectName(name)
		if err == nil {
			t.Errorf("validateProjectName(%q) accepted unsafe name", name)
			continue
		}
		if code := exitCodeFor(t, err); code != 2 {
			t.Errorf("validateProjectName(%q) code = %d, want 2", name, code)
		}
	}
	for _, name := range []string{"repo", "repo-name", "repo_name"} {
		if err := validateProjectName(name); err != nil {
			t.Errorf("validateProjectName(%q) failed: %v", name, err)
		}
	}
}

func TestValidateProjectURL(t *testing.T) {
	for _, url := range []string{"https://github.com/org/repo", "git@github.com:org/repo.git", "ssh://git@example.com/repo", "git://example.com/repo"} {
		if err := validateProjectURL(url); err != nil {
			t.Errorf("validateProjectURL(%q) failed: %v", url, err)
		}
	}
	for _, url := range []string{"", "local", "/tmp/repo", "file:///tmp/repo", "http://github.com/org/repo"} {
		err := validateProjectURL(url)
		if err == nil {
			t.Errorf("validateProjectURL(%q) accepted invalid URL", url)
			continue
		}
		if code := exitCodeFor(t, err); code != 2 {
			t.Errorf("validateProjectURL(%q) code = %d, want 2", url, code)
		}
	}
}

func TestValidateProjectMode(t *testing.T) {
	for _, mode := range []string{"no-mistakes", "direct-pr", "local-only"} {
		if err := validateProjectMode(mode); err != nil {
			t.Errorf("validateProjectMode(%q) failed: %v", mode, err)
		}
	}
	err := validateProjectMode("unexpected")
	if err == nil {
		t.Fatal("validateProjectMode accepted unexpected mode")
	}
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestProjectAddRemovesIncompleteCloneOnGitFailure(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	gitPath := filepath.Join(bin, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nmkdir -p \"$3\"\nprintf partial > \"$3/partial\"\necho clone failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(home)

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "git clone failed") {
		t.Fatalf("project add error = %v, want git clone failure", err)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "repo")); !os.IsNotExist(err) {
		t.Fatalf("incomplete clone still exists: %v", err)
	}
}

func TestProjectAddRefusesExistingCloneDestination(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, "projects", "repo")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "keep-me")
	if err := os.WriteFile(marker, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	gitPath := filepath.Join(bin, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\necho git clone should not run >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(home)

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("project add error = %v, want existing destination error", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "user data" {
		t.Fatalf("existing clone changed: %q, %v", got, err)
	}
}

func TestProjectAddRefusesAlreadyRegistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	if err := project.Add(home, project.Project{Name: "repo", URL: "https://example.com/org/repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectAddCmd()
	cmd.SetArgs([]string{"https://example.com/org/repo.git", "--mode", "local-only"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v, want already registered", err)
	}
}

func TestProjectRemoveRefusesActiveTasks(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/org/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectRemoveCmd()
	cmd.SetArgs([]string{"myproj"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), "active tasks") {
		t.Fatalf("err = %v, want active tasks referencing it", err)
	}
}

func TestProjectRemoveRefusesUnregistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	cmd := newProjectRemoveCmd()
	cmd.SetArgs([]string{"missing-proj"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
	if !strings.Contains(err.Error(), `project "missing-proj" not registered`) {
		t.Fatalf("err = %v, want not registered", err)
	}
}

func TestProjectListColumnsDoNotMergeAtFieldWidth(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	url := "https://github.com/atqamz/secondhand.git"
	if err := project.Add(home, project.Project{Name: "secondhand", URL: url, Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), url+" ") {
		t.Fatalf("project list output = %q, want %q followed by a column separator before the mode", out.String(), url)
	}
	if strings.Contains(out.String(), url+"no-mistakes") {
		t.Fatalf("project list output = %q, URL and mode columns merged", out.String())
	}
}

func TestProjectListMarksGateNotInitialized(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(gate: not initialized)") {
		t.Fatalf("project list output = %q, want a not-initialized gate marker", out.String())
	}
}

func TestProjectListJSONIncludesGateIssue(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--json"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"gate_issue": "not initialized"`) {
		t.Fatalf("project list --json output = %q, want gate_issue: not initialized", out.String())
	}
}

func TestProjectListOmitsGateMarkerWhenGateReady(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "gate: ready\n\n  no active run"))
	t.Chdir(home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "(gate:") {
		t.Fatalf("project list output = %q, want no gate marker for a ready gate", out.String())
	}
}

func TestReserveCloneDestinationIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects", "repo")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	go func() { results <- reserveCloneDestination(path) }()
	go func() { results <- reserveCloneDestination(path) }()

	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("reserveCloneDestination successes = %d, want 1", successes)
	}
}

func TestHasActiveTasksForProjectFailsOnMalformedState(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hasActiveTasksForProject(home, "repo"); err == nil {
		t.Fatal("expected malformed state to fail closed")
	}
}

func TestHasActiveTasksForProjectFailsOnIncompleteState(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state", "incomplete.json"), []byte(`{"id":"task"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hasActiveTasksForProject(home, "repo"); err == nil {
		t.Fatal("expected incomplete state to fail closed")
	}
}

func TestResolveInitHome(t *testing.T) {
	cwd := "/workspace"
	if got, err := resolveInitHome(cwd, nil); err != nil || got != cwd {
		t.Fatalf("resolveInitHome(cwd, nil) = %q, %v", got, err)
	}
	if got, err := resolveInitHome(cwd, []string{"fleet"}); err != nil || got != filepath.Join(cwd, "fleet") {
		t.Fatalf("resolveInitHome relative = %q, %v", got, err)
	}
	if _, err := resolveInitHome(cwd, []string{"one", "two"}); err == nil {
		t.Fatal("expected more than one init path to fail")
	} else if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
	if _, err := resolveInitHome(cwd, []string{"  "}); err == nil {
		t.Fatal("expected blank init path to fail")
	} else if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestReadSetupChoice(t *testing.T) {
	choice, err := readSetupChoice(strings.NewReader("2\n"), []string{"claude", "codex"}, "harness")
	if err != nil || choice != "codex" {
		t.Fatalf("readSetupChoice = %q, %v", choice, err)
	}
	if _, err := readSetupChoice(strings.NewReader("3\n"), []string{"claude", "codex"}, "harness"); err == nil {
		t.Fatal("expected out-of-range setup choice to fail")
	}
}

func TestUpdateDashboardProjects(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "projects.md"), []byte("# Projects\n\n- repo: https://example.com/repo mode=direct-pr\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "repo", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	if err := updateDashboardProjects(home); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, "data", "dashboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := dashboard.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Projects) != 1 || d.Projects[0].Name != "repo" || d.Projects[0].Mode != "direct-pr" || d.Projects[0].ActiveTaskCount != 1 {
		t.Fatalf("Projects = %+v", d.Projects)
	}
}
