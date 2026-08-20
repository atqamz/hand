package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercises init, a real git clone through project add, and doctor back to
// back with no faked tool: the one path every OS in the CI matrix, windows
// included, runs for real rather than skipping past behind a fake.
func TestSmokeInitProjectAddDoctorLifecycle(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	if err := os.WriteFile(filepath.Join(remote, "treehouse.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, remote, "add", "treehouse.toml")
	runGitIn(t, remote, "commit", "-q", "-m", "add treehouse.toml")

	url := "https://example.com/org/repo.git"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+remote+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", url)

	home := t.TempDir()
	t.Setenv("HAND_HOME", "")
	t.Chdir(home)

	if err := newInitCmd().Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	addCmd := newProjectAddCmd()
	addCmd.SetArgs([]string{url, "--mode", "local-only"})
	if err := addCmd.Execute(); err != nil {
		t.Fatalf("project add: %v", err)
	}

	clonePath := filepath.Join(home, "projects", "repo")
	if _, err := os.Stat(filepath.Join(clonePath, "treehouse.toml")); err != nil {
		t.Fatalf("clone missing treehouse.toml: %v", err)
	}

	if err := newDoctorCmd(stableBuild("v0.1.0")).Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
}
