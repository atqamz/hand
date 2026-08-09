package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
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
	mkFleetDirs(t, home)

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
	mkFleetDirs(t, home)

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
	mkFleetDirs(t, home)
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
	mkFleetDirs(t, home)
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
	mkFleetDirs(t, home)

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

func TestProjectUpstreamDeclaresAndClears(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "fork", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectUpstreamCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"fork", "https://github.com/kunchenguid/no-mistakes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: upstream-set\nupstream: kunchenguid/no-mistakes\n") {
		t.Fatalf("out = %q, want the declared upstream confirmed", out.String())
	}

	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Upstream != "kunchenguid/no-mistakes" {
		t.Fatalf("got %+v, want the upstream normalized and stored", projects)
	}

	clear := newProjectUpstreamCmd()
	var cleared strings.Builder
	clear.SetOut(&cleared)
	clear.SetArgs([]string{"fork", ""})
	if err := clear.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cleared.String(), "result: upstream-cleared\nupstream: none\n") {
		t.Fatalf("out = %q, want the clear confirmed", cleared.String())
	}
	projects, err = project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].Upstream != "" {
		t.Fatalf("upstream = %q, want it cleared", projects[0].Upstream)
	}
}

func TestProjectUpstreamRejectsUnresolvableRepo(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "fork", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}

	// "kunchen guid/no-mistakes" parses as a slug but cannot survive the registry
	// projection, which separates fields by whitespace: stored, it would come back
	// truncated and reject the whole line for every later project command.
	for _, repo := range []string{"no-mistakes", "kunchen guid/no-mistakes"} {
		cmd := newProjectUpstreamCmd()
		cmd.SetArgs([]string{"fork", repo})
		err := cmd.Execute()
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("upstream %q: got %v, want ExitError code 2", repo, err)
		}

		projects, listErr := project.List(home)
		if listErr != nil {
			t.Fatalf("upstream %q: %v", repo, listErr)
		}
		if projects[0].Upstream != "" {
			t.Fatalf("upstream = %q, want nothing recorded from a refused ref %q", projects[0].Upstream, repo)
		}
	}
}

func TestProjectUpstreamRefusesUnregistered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectUpstreamCmd()
	cmd.SetArgs([]string{"missing-proj", "owner/repo"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestProjectListShowsUpstreamAlongsideAGateMarker(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "gated", "kunchenguid/no-mistakes"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)"))
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ",kunchenguid/no-mistakes,not initialized\n") {
		t.Fatalf("project list output = %q, want the upstream and the gate issue in their own columns", out.String())
	}

	jsonCmd := newProjectListCmd()
	jsonCmd.SetArgs([]string{"--json"})
	var jsonOut strings.Builder
	jsonCmd.SetOut(&jsonOut)
	if err := jsonCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"upstream": "kunchenguid/no-mistakes"`) {
		t.Fatalf("project list --json output = %q, want the upstream field", jsonOut.String())
	}
}

// A repo URL carries the two characters a TOON row would otherwise read as
// structure, so it has to come back quoted rather than splitting its own cell.
func TestProjectListQuotesAURLRatherThanLettingItSplitTheRow(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	url := "https://github.com/atqamz/secondhand.git"
	if err := project.Add(home, project.Project{Name: "secondhand", URL: url, Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--fields", "name,url,mode"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	want := "projects[1]{name,url,mode}:\n  secondhand,\"" + url + "\",no-mistakes\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("project list output = %q, want %q", out.String(), want)
	}
}

func TestProjectListFieldsRejectsAnUnknownName(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	cmd.SetArgs([]string{"--fields", "nope"})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if got := exitCodeFor(t, cmd.Execute()); got != 2 {
		t.Fatalf("exit code = %d, want 2 for an unknown field", got)
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
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gate_issues: 1\n") || !strings.Contains(out.String(), ",not initialized\n") {
		t.Fatalf("project list output = %q, want a not-initialized gate marker and the aggregate counting it", out.String())
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
	mkFleetDirs(t, home)

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

func TestProjectListStatesNoGateIssueWhenGateReady(t *testing.T) {
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
	mkFleetDirs(t, home)

	cmd := newProjectListCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gate_issues: 0\n") || !strings.Contains(out.String(), ",none\n") {
		t.Fatalf("project list output = %q, want a ready gate stated as no issue rather than as an absent marker", out.String())
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

func TestWarnHandHomeMismatch(t *testing.T) {
	home := t.TempDir()

	t.Setenv("HAND_HOME", "")
	var quiet strings.Builder
	if err := warnHandHomeMismatch(&quiet, home); err != nil {
		t.Fatal(err)
	}
	if quiet.String() != "" {
		t.Fatalf("got %q, want no warning without HAND_HOME", quiet.String())
	}

	t.Setenv("HAND_HOME", home)
	quiet.Reset()
	if err := warnHandHomeMismatch(&quiet, home); err != nil {
		t.Fatal(err)
	}
	if quiet.String() != "" {
		t.Fatalf("got %q, want no warning when HAND_HOME is the initialized home", quiet.String())
	}

	elsewhere := t.TempDir()
	t.Setenv("HAND_HOME", elsewhere)
	var warned strings.Builder
	if err := warnHandHomeMismatch(&warned, home); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warned.String(), elsewhere) || !strings.Contains(warned.String(), home) {
		t.Fatalf("got %q, want a warning naming both %q and %q", warned.String(), elsewhere, home)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HAND_HOME", "fleet")
	absHandHome := filepath.Join(cwd, "fleet")
	var relative strings.Builder
	if err := warnHandHomeMismatch(&relative, home); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("warning: HAND_HOME is set to %s, so every other hand command will use that home, not %s\n", absHandHome, home)
	if relative.String() != want {
		t.Fatalf("got %q, want %q", relative.String(), want)
	}
}
