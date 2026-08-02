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
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRefreshSkipsSilentlyWhenNotAFleetHome(t *testing.T) {
	dir := t.TempDir()
	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed {
		t.Fatal("got refreshed=true, want false outside a fleet home")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("got AGENTS.md written outside a fleet home, err=%v", err)
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
	path := filepath.Join(dir, "AGENTS.md")

	userPreamble := "# House rules\n\nRead this before the generated block.\n\n"
	userContent := "\n- a project-specific rule the user wrote by hand\n\n## Maintaining this file\n\nKeep this file tidy.\n"
	stale := userPreamble + beginMarker + "\n# Secondhand\n\nAn out-of-date template.\n" + endMarker + userContent
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed {
		t.Fatal("got refreshed=false, want true when the generated block was stale")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), userPreamble) {
		t.Fatalf("got %q, want user content before the markers preserved verbatim", got)
	}
	if !strings.HasSuffix(string(got), userContent) {
		t.Fatalf("got %q, want user content after the markers preserved verbatim", got)
	}
	if !strings.Contains(string(got), "## Workflow") {
		t.Fatalf("got %q, want the current generated Workflow section", got)
	}
	if strings.Contains(string(got), "An out-of-date template.") {
		t.Fatalf("got %q, want the stale generated block replaced", got)
	}
}

func TestRefreshLeavesUnmarkedAgentsMdUntouched(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")
	handWritten := "# Hand-written AGENTS.md with no generated markers\n"
	if err := os.WriteFile(path, []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed {
		t.Fatal("got refreshed=true, want false when the template was not updated")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != handWritten {
		t.Fatalf("got %q, want unchanged %q", got, handWritten)
	}
}

// A marker-less legacy AGENTS.md, or one already carrying the current template,
// must not be rewritten at all: an identical-bytes write still swaps the inode,
// resets the mode, and turns a symlinked AGENTS.md into a regular file.
func TestRefreshLeavesUpToDateFileOnDiskUntouched(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed {
		t.Fatal("got refreshed=true, want false when the template is already current")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("got AGENTS.md replaced, want the existing file left untouched")
	}
	if after.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want the existing 0600 preserved", after.Mode().Perm())
	}
}

// This repo keeps a hand-maintained copy of the generated block in its own
// AGENTS.md instead of deriving it, so a rule edited in one copy and not the
// other drifts silently until the duplication itself is removed.
func TestGeneratedRulesMatchThisRepoAgentsMdCopy(t *testing.T) {
	repoCopy, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	absolutePathRule := "- Name a path in a brief, a status report, or an operator message: full and absolute, never relative. `hand` resolves the home from the current working directory, and a project clone can share its name with the home itself, so a relative path resolves against whichever directory happens to be current.\n"
	if !strings.Contains(generatedBody, absolutePathRule) {
		t.Fatalf("got generated body %q, want the absolute-path rule verbatim", generatedBody)
	}
	if !strings.Contains(string(repoCopy), absolutePathRule) {
		t.Fatalf("got repo AGENTS.md %q, want the same absolute-path rule byte-for-byte", repoCopy)
	}

	_, generatedRules, ok := strings.Cut(generatedBody, "## Rules\n")
	if !ok {
		t.Fatalf("got generated body %q, want a Rules section", generatedBody)
	}
	_, repoRules, ok := strings.Cut(string(repoCopy), "## Rules\n")
	if !ok {
		t.Fatalf("got repo AGENTS.md %q, want a Rules section", repoCopy)
	}
	if !strings.HasPrefix(repoRules, generatedRules) {
		t.Fatalf("got repo rules %q, want them to open with the generated rules %q", repoRules, generatedRules)
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
