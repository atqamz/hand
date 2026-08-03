package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

func assertExitCode2(t *testing.T, err error) {
	t.Helper()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("got %v, want ExitError code 2", err)
	}
}

func addOriginRemote(t *testing.T, dir, url string) {
	t.Helper()
	c := exec.Command("git", "remote", "add", "origin", url)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git remote add origin failed: %v: %s", err, out)
	}
}

func writeFakeGhPRView(t *testing.T, exitCode int) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if exitCode != 0 {
		script += "echo 'gh: pull request not found' >&2\n"
		script += fmt.Sprintf("exit %d\n", exitCode)
	} else {
		script += "printf '{\"state\":\"OPEN\"}'\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func setupPRHome(t *testing.T) (home, clonePath string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	clonePath = filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	return home, clonePath
}

func TestPRRejectsMalformedURL(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://example.com/not/github"})
	err := cmd.Execute()
	assertExitCode2(t, err)
}

func TestPRRefusesWhenTaskMissing(t *testing.T) {
	setupPRHome(t)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"missing-task", "https://github.com/a/b/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

func TestPRRefusesDifferentAlreadyRecordedPR(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", PR: "https://github.com/a/b/pull/1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/a/b/pull/2"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/a/b/pull/1" {
		t.Fatalf("task.PR = %q, want the original PR left untouched", task.PR)
	}
}

// TestPRReconcilesWhenSameURLAlreadyRecorded pins the reconciling repeat: an
// operator retrying this command after the URL already made it into task
// state gets a friendly no-op instead of an error. The project is deliberately
// unregistered: reaching validation would exit 3, so passing also proves it is
// skipped for a URL already on record.
func TestPRReconcilesWhenSameURLAlreadyRecorded(t *testing.T) {
	home, _ := setupPRHome(t)
	url := "https://github.com/a/b/pull/1"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered", PR: url}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", url})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already recorded") {
		t.Fatalf("out = %q, want an already-recorded message", out.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want %q left in place", task.PR, url)
	}
}

func TestPRRefusesWhenProjectNotRegistered(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/a/b/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

func TestPRRefusesWhenRepoMismatch(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/other-repo.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/other-repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

// A fork contribution's PR lives on the upstream repo, never on the fork hand
// pushed to, so the guard has to accept the declared upstream - and only that
// one. The pair is kept in one test so the accepting and the refusing case share
// an identical project, leaving the declaration as the only difference between
// them.
func TestPRAcceptsTheDeclaredUpstreamAndStillRefusesAnyOtherRepo(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/atqamz/no-mistakes.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "demo", "kunchenguid/no-mistakes"); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-2", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 0)

	upstreamPR := "https://github.com/kunchenguid/no-mistakes/pull/597"
	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", upstreamPR})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("recording a PR on the declared upstream: %v", err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != upstreamPR {
		t.Fatalf("task.PR = %q, want %q", task.PR, upstreamPR)
	}

	unrelated := newPRCmd()
	unrelated.SetArgs([]string{"task-2", "https://github.com/someone/else/pull/1"})
	assertExitCode3(t, unrelated.Execute())
	other, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if other.PR != "" {
		t.Fatalf("task.PR = %q, want a repo that is neither the project's nor its upstream refused", other.PR)
	}
}

func TestPRRefusesWhenGhReportsNotFound(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 1)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want no PR recorded when gh can't confirm it exists", task.PR)
	}
}

func TestPRRecordsSuccessfully(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 0)

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "recorded PR for task-1: https://github.com/owner/secondhand/pull/1") {
		t.Fatalf("out = %q, want a recorded confirmation", out.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/owner/secondhand/pull/1" {
		t.Fatalf("task.PR = %q, want the URL recorded", task.PR)
	}
}
