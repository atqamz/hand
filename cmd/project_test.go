package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProjectName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\\name`, "foo:bar", "repo ", "repo=one"} {
		if err := validateProjectName(name); err == nil {
			t.Errorf("validateProjectName(%q) accepted unsafe name", name)
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
		if err := validateProjectURL(url); err == nil {
			t.Errorf("validateProjectURL(%q) accepted invalid URL", url)
		}
	}
}

func TestValidateProjectMode(t *testing.T) {
	for _, mode := range []string{"no-mistakes", "direct-pr", "local-only"} {
		if err := validateProjectMode(mode); err != nil {
			t.Errorf("validateProjectMode(%q) failed: %v", mode, err)
		}
	}
	if err := validateProjectMode("unexpected"); err == nil {
		t.Fatal("validateProjectMode accepted unexpected mode")
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
