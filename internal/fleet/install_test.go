package fleet_test

import (
	"errors"
	"io/fs"
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
		if err := fleet.Install(home, "hand"); err != nil {
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
	if err := fleet.Install(home, "hand"); err != nil {
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
		err := fleet.Install(home, "hand")
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
	if err := fleet.Install(home, "hand"); err == nil {
		t.Fatal("install followed a symlink out of the home")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("install wrote %d entries outside the home", len(entries))
	}
}

func TestInstallSpeaksTheCommandName(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "state", "hand.db"), "0.7")
	if err := fleet.Install(home, "hand-next"); err != nil {
		t.Fatal(err)
	}
	agents := read(t, filepath.Join(home, "AGENTS.md"))
	skill := read(t, filepath.Join(home, skillPaths[0]))
	migrate := read(t, filepath.Join(home, ".claude/skills/secondhand-migrate/SKILL.md"))
	for name, text := range map[string]string{"AGENTS.md": agents, "SKILL.md": skill, "migrate SKILL.md": migrate} {
		if !strings.Contains(text, "`hand-next orient`") || strings.Contains(text, "`hand ") || strings.Contains(text, "`hand`") {
			t.Fatalf("%s does not name hand-next throughout:\n%s", name, text)
		}
	}
	if err := fleet.Install(home, "hand"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(home, "AGENTS.md")) != skills.Bootstrap {
		t.Fatal("a later init under the plain name did not restore the plain bootstrap")
	}
}

var migratePaths = []string{".claude/skills/secondhand-migrate/SKILL.md", ".agents/skills/secondhand-migrate/SKILL.md", ".grok/skills/secondhand-migrate/SKILL.md", ".pi/skills/secondhand-migrate/SKILL.md"}

func TestInstallAddsTheMigrateSkillOnlyBesideA07Fleet(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "state", "hand.db"), "0.7")
	if err := fleet.Install(home, "hand-next"); err != nil {
		t.Fatal(err)
	}
	for _, p := range migratePaths {
		if got := read(t, filepath.Join(home, p)); !strings.Contains(got, "name: secondhand-migrate") || strings.Contains(got, "`hand ") {
			t.Fatalf("%s = %q", p, got)
		}
	}
	if err := os.Remove(filepath.Join(home, "state", "hand.db")); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Install(home, "hand-next"); err != nil {
		t.Fatal(err)
	}
	for _, p := range migratePaths {
		if _, err := os.Stat(filepath.Dir(filepath.Join(home, p))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s left behind: %v", p, err)
		}
	}
}

func TestInstallLeavesAForeignMigrateSkillAlone(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, migratePaths[0]), "mine\n")
	if err := fleet.Install(home, "hand"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(home, migratePaths[0])) != "mine\n" {
		t.Fatal("a foreign migrate skill was removed")
	}
	write(t, filepath.Join(home, "state", "hand.db"), "0.7")
	if err := fleet.Install(home, "hand"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("install over a foreign migrate skill = %v", err)
	}
}

func TestADirectoryIsNotThe07Marker(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state", "hand.db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Install(home, "hand"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, migratePaths[0])); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a directory counted as the 0.7 marker: %v", err)
	}
}

func TestAnUnreadable07MarkerKeepsTheMigrateSkill(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "state", "hand.db"), "0.7")
	if err := fleet.Install(home, "hand"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, "state"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "state"), 0o755) })
	if err := fleet.Install(home, "hand"); err == nil {
		t.Fatal("install ignored an unreadable 0.7 marker")
	}
	if _, err := os.Stat(filepath.Join(home, migratePaths[0])); err != nil {
		t.Fatalf("the migrate skill was removed: %v", err)
	}
}
