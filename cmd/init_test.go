package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/home"
)

func TestInitCreatesTheHandDbMarker(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "state", "hand.db")); err != nil {
		t.Fatalf("state/hand.db missing after init: %v", err)
	}
	ok, err := home.IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("got IsHome false right after init, want true")
	}
}

func TestInitSeedsEveryDataSkeletonFile(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"data/backlog.md":      "# Backlog",
		"data/projects.md":     "# Projects",
		"data/operator.md":     "## Hard constraints",
		"data/learnings.md":    "# Learnings",
		"data/done-archive.md": "# Done archive",
		"data/note-archive.md": "# Note archive",
	}
	for rel, header := range want {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("%s missing after init: %v", rel, err)
		}
		if !strings.Contains(string(got), header) {
			t.Fatalf("%s = %q, want it to contain %q", rel, got, header)
		}
	}
}

// A home initialized before the layout gained a file picks the file up by
// re-running init, which must never cost it the content of one it already has.
func TestInitLeavesExistingDataFilesAlone(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "# Operator\n\n## Authority\n\nMerge without asking.\n"
	if err := os.WriteFile(filepath.Join(dir, "data", "operator.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "data", "operator.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("data/operator.md = %q, want unchanged %q", got, existing)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "learnings.md")); err != nil {
		t.Fatalf("data/learnings.md missing: %v", err)
	}
}

// Whoever hits a seeding failure reads the message to decide where to look, so
// it names every file that failed and says the same thing on every run.
func TestInitSkeletonFilesReportsEveryFailureInAStableOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	first := initSkeletonFiles(dir)
	if first == nil {
		t.Fatal("got nil, want an error when data/ is not a directory")
	}
	second := initSkeletonFiles(dir)
	if second == nil || second.Error() != first.Error() {
		t.Fatalf("run 2 = %v, want the same message as run 1 %v", second, first)
	}

	for _, rel := range []string{"data/backlog.md", "data/projects.md", "data/operator.md", "data/learnings.md", "data/done-archive.md", "data/note-archive.md"} {
		if !strings.Contains(first.Error(), rel) {
			t.Fatalf("got %q, want it to name %s", first, rel)
		}
	}
}

func TestInitIsIdempotentAboutTheHandDbMarker(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	for i := 0; i < 2; i++ {
		cmd := newInitCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "state", "hand.db")); err != nil {
		t.Fatalf("state/hand.db missing after repeat init: %v", err)
	}
}

func TestInitInstallsTheSessionHookAndSaysSoOnlyWhenItWroteIt(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	for i, want := range []string{"session_hook: written\n", "session_hook: unchanged\n"} {
		cmd := newInitCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("run %d output = %q, want it to contain %q", i+1, out.String(), want)
		}
	}

	settings, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "SessionStart") {
		t.Fatalf("settings = %q, want a SessionStart hook in it", settings)
	}
}
