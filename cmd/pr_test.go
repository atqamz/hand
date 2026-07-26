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

	"github.com/atqamz/secondhand/internal/dashboard"
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
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "dashboard.md"), []byte(dashboardSkeleton), 0o644); err != nil {
		t.Fatal(err)
	}
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

// TestPRReconcilesTheDashboardWhenSameURLAlreadyRecorded pins the reconciling
// repeat. An auto-record can write the URL into task state and then fail at the
// dashboard, and the pr-not-recorded event reporting that names this command as
// the remedy - so a no-op here would exit 0 while leaving the dashboard's PR
// column empty with no signal left. The project is deliberately unregistered:
// reaching validation would exit 3, so passing also proves it is skipped for a
// URL already on record.
func TestPRReconcilesTheDashboardWhenSameURLAlreadyRecorded(t *testing.T) {
	home, _ := setupPRHome(t)
	url := "https://github.com/a/b/pull/1"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered", PR: url}); err != nil {
		t.Fatal(err)
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{AddActiveTask: &dashboard.ActiveTask{
		ID: "task-1", Project: "demo", Kind: "ship", State: "working", Age: "just now",
	}}); err != nil {
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

	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), url) {
		t.Fatalf("dashboard.md = %q, want the PR column reconciled rather than left empty", string(data))
	}
}

// TestPRReportsWhenThereIsNoDashboardRowToReconcile is the other half of the
// reconciling repeat. Exiting 0 with "(dashboard reconciled)" when no row
// matched would claim the repair this command is the documented remedy for
// while the PR column stays exactly as empty as before - the silent success the
// reconcile path exists to remove. The row is never fabricated: active rows come
// from hand spawn.
func TestPRReportsWhenThereIsNoDashboardRowToReconcile(t *testing.T) {
	home, _ := setupPRHome(t)
	url := "https://github.com/a/b/pull/1"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered", PR: url}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", url})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "no active row") {
		t.Fatalf("err = %v, want the missing dashboard row named", err)
	}
	if strings.Contains(out.String(), "reconciled") {
		t.Fatalf("out = %q, want no claim of a repair that did not happen", out.String())
	}

	dashPath := filepath.Join(home, "data", "dashboard.md")
	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "task-1") {
		t.Fatalf("dashboard.md = %q, want no row invented for a task hand spawn never added", string(data))
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want the recorded PR left intact - the durable truth is unaffected", task.PR)
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

	dashPath := filepath.Join(home, "data", "dashboard.md")
	if err := dashboard.Update(dashPath, dashboard.UpdateOpts{AddActiveTask: &dashboard.ActiveTask{
		ID: "task-1", Project: "demo", Kind: "ship", State: "working", Age: "just now",
	}}); err != nil {
		t.Fatal(err)
	}

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

	data, err := os.ReadFile(dashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://github.com/owner/secondhand/pull/1") {
		t.Fatalf("dashboard.md = %q, want the PR column updated", string(data))
	}
}
