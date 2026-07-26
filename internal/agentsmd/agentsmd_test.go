package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "dashboard.md"), []byte("# Dashboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIsWorkspaceTrueWhenDashboardExists(t *testing.T) {
	dir := makeWorkspace(t)
	got, err := IsWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("got false, want true")
	}
}

func TestIsWorkspaceFalseWhenNotInitialized(t *testing.T) {
	dir := t.TempDir()
	got, err := IsWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want false")
	}
}

func TestRefreshSkipsSilentlyWhenNotWorkspace(t *testing.T) {
	dir := t.TempDir()
	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed {
		t.Fatal("got refreshed=true, want false outside a workspace")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("got AGENTS.md written outside a workspace, err=%v", err)
	}
}

func TestRefreshWritesAgentsMdAndClaudeSymlinkWhenMissing(t *testing.T) {
	dir := makeWorkspace(t)

	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed {
		t.Fatal("got refreshed=false, want true")
	}

	got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), beginMarker) || !strings.Contains(string(got), endMarker) {
		t.Fatalf("got %q, want generated markers present", got)
	}
	if !strings.Contains(string(got), "## Workflow") || !strings.Contains(string(got), "## Rules") {
		t.Fatalf("got %q, want Workflow and Rules sections", got)
	}

	link, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if link != "AGENTS.md" {
		t.Fatalf("got CLAUDE.md -> %q, want AGENTS.md", link)
	}
}

// This is the requirement most likely to regress silently: a refresh must
// never wipe out rules or sections the user appended by hand.
func TestRefreshPreservesUserAddedContentAcrossRefresh(t *testing.T) {
	dir := makeWorkspace(t)

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "AGENTS.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	userContent := "- a project-specific rule the user wrote by hand\n\n## Maintaining this file\n\nKeep this file tidy.\n"
	withUserContent := string(original) + userContent
	if err := os.WriteFile(path, []byte(withUserContent), 0o644); err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed {
		t.Fatal("got refreshed=false, want true")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), userContent) {
		t.Fatalf("got %q, want user-added content preserved verbatim", got)
	}
	if !strings.Contains(string(got), "## Workflow") {
		t.Fatalf("got %q, want generated Workflow section still present", got)
	}
}

func TestRefreshLeavesUnmarkedAgentsMdUntouched(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")
	handWritten := "# Hand-written AGENTS.md with no generated markers\n"
	if err := os.WriteFile(path, []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != handWritten {
		t.Fatalf("got %q, want unchanged %q", got, handWritten)
	}
}

func TestRefreshDoesNotOverwriteExistingClaudeSymlink(t *testing.T) {
	dir := makeWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "OTHER.md"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("OTHER.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	link, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if link != "OTHER.md" {
		t.Fatalf("got CLAUDE.md -> %q, want unchanged OTHER.md", link)
	}
}
