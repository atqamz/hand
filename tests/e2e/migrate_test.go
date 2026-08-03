//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/store"
)

// A fleet home the way a pre-sqlite hand left one. Deliberately not built by
// hand init: the point of the migration is that it meets a home hand did not
// create.
func legacyHome(t *testing.T) string {
	t.Helper()
	isolateGitConfig(t)
	home := t.TempDir()
	for _, sub := range []string{"data", "state"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("data", "projects.md"), "# Projects\n\n- demo: https://example.com/demo.git mode=direct-pr\n")
	write(filepath.Join("state", "task-1.json"), `{
		"id": "task-1",
		"project": "demo",
		"kind": "ship",
		"harness": "claude",
		"worktree": "/nonexistent/wt-1",
		"herdr": {"session": "default", "tab_id": "wA:tB", "pane_id": "wA:pB"},
		"pr": "https://github.com/owner/demo/pull/7",
		"report_offset": 42,
		"created_at": "2026-07-24T10:00:00Z"
	}`)
	return home
}

// The migration has to happen under a hand that has never seen this home
// before, with no previous binary and no explicit import step, and a second
// run is what actually happens on a real fleet.
func TestMigrationImportsALegacyHomeAndIsIdempotent(t *testing.T) {
	home := legacyHome(t)

	first := runHand(t, home, "status", "task-1")
	if first.code != 0 {
		t.Fatalf("status after migration: exit %d, stderr %q", first.code, first.stderr)
	}
	for _, want := range []string{"task-1", "demo", "https://github.com/owner/demo/pull/7", "wA:tB"} {
		if !strings.Contains(first.stdout, want) {
			t.Fatalf("status stdout = %q, want the imported %q", first.stdout, want)
		}
	}

	if _, err := os.Stat(filepath.Join(home, "state", "task-1.json")); !os.IsNotExist(err) {
		t.Fatalf("stat state/task-1.json: %v, want the imported file moved aside", err)
	}
	archived := filepath.Join(store.LegacyDir(home), "task-1.json")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("stat %s: %v, want the imported file kept for the operator", archived, err)
	}
	if _, err := os.Stat(store.Path(home)); err != nil {
		t.Fatalf("stat machine state database: %v", err)
	}

	projects := runHand(t, home, "project", "list")
	if projects.code != 0 || !strings.Contains(projects.stdout, "demo") {
		t.Fatalf("project list: exit %d, stdout %q, want the registry imported from prose", projects.code, projects.stdout)
	}

	second := runHand(t, home, "status")
	if second.code != 0 {
		t.Fatalf("second status: exit %d, stderr %q", second.code, second.stderr)
	}
	if got := strings.Count(second.stdout, "task-1"); got != 1 {
		t.Fatalf("status stdout = %q, want task-1 listed once after a second run, got %d", second.stdout, got)
	}
	againProjects := runHand(t, home, "project", "list")
	if got := strings.Count(againProjects.stdout, "https://example.com/demo.git"); got != 1 {
		t.Fatalf("project list = %q, want demo listed once after a second run, got %d", againProjects.stdout, got)
	}
}

// A legacy file that reappears in state/ after the import - restored from a
// backup, or copied back by an operator reading it - is a snapshot from before
// the import, so it must never overwrite what hand has recorded since.
func TestMigrationDoesNotLetARestoredLegacyFileOverwriteNewerState(t *testing.T) {
	home := legacyHome(t)
	if got := runHand(t, home, "status", "task-1"); got.code != 0 {
		t.Fatalf("status after migration: exit %d, stderr %q", got.code, got.stderr)
	}

	restored, err := os.ReadFile(filepath.Join(store.LegacyDir(home), "task-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(restored), "/pull/7", "/pull/999", 1)
	if err := os.WriteFile(filepath.Join(home, "state", "task-1.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runHand(t, home, "status", "task-1")
	if got.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, "/pull/999") {
		t.Fatalf("status stdout = %q, want the database to win over a restored snapshot", got.stdout)
	}
	if !strings.Contains(got.stdout, "/pull/7") {
		t.Fatalf("status stdout = %q, want the recorded PR untouched", got.stdout)
	}
}

// A legacy file hand cannot parse stops the import and names the file, rather
// than importing the rest and leaving an operator to notice a task went missing.
func TestMigrationRefusesLoudlyOnAnUnparseableLegacyFile(t *testing.T) {
	home := legacyHome(t)
	if err := os.WriteFile(filepath.Join(home, "state", "task-2.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runHand(t, home, "status")
	if got.code == 0 {
		t.Fatalf("status: exit 0, want a refusal, stdout %q", got.stdout)
	}
	if !strings.Contains(got.stderr, filepath.Join("state", "task-2.json")) {
		t.Fatalf("stderr = %q, want the unparseable file named", got.stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "task-1.json")); err != nil {
		t.Fatalf("stat state/task-1.json: %v, want a refused import to leave every legacy file in place", err)
	}
}
