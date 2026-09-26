package fleet_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/skills"
)

var skillPaths = []string{".claude/skills/secondhand/SKILL.md", ".agents/skills/secondhand/SKILL.md", ".grok/skills/secondhand/SKILL.md", ".pi/skills/secondhand/SKILL.md"}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallWritesTheBootstrapAndTheSkill(t *testing.T) {
	home := t.TempDir()
	for range 2 {
		if err := fleet.Install(home); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(home, "AGENTS.md")); got != skills.Bootstrap {
			t.Fatalf("AGENTS.md = %q", got)
		}
		if got := read(t, filepath.Join(home, "CLAUDE.md")); got != "@AGENTS.md\n" {
			t.Fatalf("CLAUDE.md = %q", got)
		}
		for _, p := range skillPaths {
			if got := read(t, filepath.Join(home, p)); got != skills.Secondhand {
				t.Fatalf("%s differs from the embedded skill", p)
			}
		}
	}
}

func TestInstallReplacesTheOldFleetBootstrap(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "AGENTS.md"), "## Secondhand supervisor bootstrap\n\nold rules\n")
	write(t, filepath.Join(home, "CLAUDE.md"), "@AGENTS.md")
	write(t, filepath.Join(home, skillPaths[0]), "---\nname: secondhand\nmetadata:\n  source: atqamz/hand\n  managed-by: hand\n---\nold skill\n")
	if err := fleet.Install(home); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(home, "AGENTS.md")) != skills.Bootstrap || read(t, filepath.Join(home, skillPaths[0])) != skills.Secondhand {
		t.Fatal("old Hand files were not replaced")
	}
}

func TestInstallRefusesForeignFilesAndWritesNothing(t *testing.T) {
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", skillPaths[1]} {
		home := t.TempDir()
		write(t, filepath.Join(home, rel), "mine\n")
		err := fleet.Install(home)
		if !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), rel) {
			t.Fatalf("%s: install = %v", rel, err)
		}
		if read(t, filepath.Join(home, rel)) != "mine\n" {
			t.Fatalf("%s was overwritten", rel)
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 1 {
			t.Fatalf("%s: install wrote %d entries, want only the foreign file", rel, len(entries))
		}
	}
}

func TestInstallNeverWritesOutsideTheHome(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Install(home); err == nil {
		t.Fatal("install followed a symlink out of the home")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("install wrote %d entries outside the home", len(entries))
	}
}
