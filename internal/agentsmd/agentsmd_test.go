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
	if err := os.WriteFile(filepath.Join(dir, "state", "hand.db"), []byte("x"), 0o644); err != nil {
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

// This repo's own AGENTS.md points at generatedBody instead of keeping a
// hand-maintained copy of it (atqamz/secondhand#44), so the only thing worth
// asserting here is that the pointer survives - not the prose around it.
func TestThisRepoAgentsMdPointsAtGeneratedBody(t *testing.T) {
	repoCopy, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repoCopy), "generatedBody") {
		t.Fatalf("got repo AGENTS.md %q, want a pointer to generatedBody", repoCopy)
	}
	if strings.Contains(string(repoCopy), beginMarker) {
		t.Fatalf("got repo AGENTS.md %q, want no generated-block markers: it is not a refresh target", repoCopy)
	}
}

// atqamz/secondhand#87's fix has to reach every worker without a new report-vocabulary word, since the
// watcher's classifier and hand status's renderer were outside that change's scope: the working: prefix
// plus a first-person convention was the only lever available.
func TestGeneratedRulesCoverSelfDecidedCallsInFirstPerson(t *testing.T) {
	if !strings.Contains(generatedBody, "hand send") || !strings.Contains(generatedBody, "operator decision") {
		t.Fatalf("got generated body %q, want the hand send invariant", generatedBody)
	}
	if !strings.Contains(generatedBody, "working: deciding myself:") {
		t.Fatalf("got generated body %q, want the first-person working: convention", generatedBody)
	}
}

// atqamz/secondhand#114: a fleet agent following the template had no reason
// to reach for hand hold, since every "waiting on" case routed through
// data/backlog.md and hand send.
func TestGeneratedRulesCoverHolds(t *testing.T) {
	if !strings.Contains(generatedBody, "hand hold set") || !strings.Contains(generatedBody, "hand hold clear") {
		t.Fatalf("got generated body %q, want hand hold set and hand hold clear", generatedBody)
	}
}

// atqamz/secondhand#47: the four files hand init seeds are inert unless the template says
// who reads each one and when. atqamz/secondhand#64: the one direction data/ does not carry
// has to be stated, or the agent invents a hand-written operator channel again.
func TestGeneratedRulesCoverOperatorContextLearningsAndArchives(t *testing.T) {
	for _, want := range []string{
		"data/operator.md",
		"data/learnings.md",
		"data/done-archive.md",
		"data/note-archive.md",
		"written for the operator to read",
		"never rewrite it",
	} {
		if !strings.Contains(generatedBody, want) {
			t.Fatalf("got generated body %q, want it to name %q", generatedBody, want)
		}
	}
	if strings.Contains(generatedBody, "data/inbox.md") {
		t.Fatalf("got generated body %q, want no hand-written operator channel", generatedBody)
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

func hasViolation(violations []Violation, substr string) bool {
	for _, v := range violations {
		if strings.Contains(v.Text, substr) {
			return true
		}
	}
	return false
}

func TestCheckSkipsNonFleetHomeAndMissingFile(t *testing.T) {
	violations, err := Check(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if violations != nil {
		t.Fatalf("got %v, want nil outside a fleet home", violations)
	}

	dir := makeWorkspace(t)
	violations, err = Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if violations != nil {
		t.Fatalf("got %v, want nil when AGENTS.md does not exist yet", violations)
	}
}

func TestCheckCleanRightAfterRefresh(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %v, want no violations right after Refresh", violations)
	}
}

func TestCheckFlagsDateOutsideGeneratedBlock(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "Fixed the race condition on 2026-07-29.")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "date outside the generated block") {
		t.Fatalf("got %v, want a date violation", violations)
	}
}

func TestCheckIgnoresDateInCodeSpanOrURL(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "Example: `2026-07-29` is an RFC3339 date, see https://example.com/releases/2026-07-29 for the tag.")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hasViolation(violations, "date outside the generated block") {
		t.Fatalf("got %v, want no date violation for a code span or URL", violations)
	}
}

func TestCheckFlagsSelfExpiringPhrasing(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "Until #84 lands, do the mtime check by hand.")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "self-expiring phrasing") {
		t.Fatalf("got %v, want a self-expiring-phrasing violation", violations)
	}
}

func TestCheckFlagsEmDashAndEmoji(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "This rule matters a lot — so don't skip it")
	appendLine(t, path, "Ship it \U0001F680")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := countViolations(violations, "banned character"); n != 2 {
		t.Fatalf("got %d banned-character violations in %v, want 2 (em dash and emoji)", n, violations)
	}
}

func TestCheckFlagsBannedCharacterInsideFence(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "```\nan example — with an em dash\n```")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := countViolations(violations, "banned character"); n != 1 {
		t.Fatalf("got %d banned-character violations in %v, want 1 inside a fenced block", n, violations)
	}
}

func countViolations(violations []Violation, substr string) int {
	n := 0
	for _, v := range violations {
		if strings.Contains(v.Text, substr) {
			n++
		}
	}
	return n
}

func TestCheckFlagsGeneratedBlockDrift(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(content), "## Rules", "## Rules (edited by hand)", 1)
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "generated block has drifted") {
		t.Fatalf("got %v, want a generated-block-drift violation", violations)
	}
	// A bare "run hand init" would target the operator's working directory,
	// which is a new nested fleet home whenever that is not the home itself.
	if !hasViolation(violations, "run hand init "+dir+" to refresh") {
		t.Fatalf("got %v, want the remedy to name the resolved home %q", violations, dir)
	}
}

func TestCheckFlagsMissingGeneratedMarkers(t *testing.T) {
	dir := makeWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("# Hand-written, no markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "no hand:generated markers") {
		t.Fatalf("got %v, want a missing-markers finding: Refresh declines to touch this file, so nothing else reports it", violations)
	}
	if hasViolation(violations, "generated block has drifted") {
		t.Fatalf("got %v, want no drift violation when there is no block to drift", violations)
	}
	for _, v := range violations {
		if strings.Contains(v.Text, "no hand:generated markers") && v.Severity != SeverityInfo {
			t.Fatalf("got severity %v for the missing-markers finding, want SeverityInfo: Check cannot tell an accidental marker-less file from a deliberate one", v.Severity)
		}
	}
}

func TestCheckFlagsUnterminatedFence(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "```")
	appendLine(t, path, "Fixed the race condition on 2026-07-29.")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "unterminated code fence") {
		t.Fatalf("got %v, want an unterminated-fence violation instead of a silently truncated scan", violations)
	}
}

func TestCheckIgnoresAwaitingWithNoIssueReference(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	appendLine(t, path, "Never merge a PR awaiting review.")

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hasViolation(violations, "self-expiring phrasing") {
		t.Fatalf("got %v, want no violation: a bare awaiting with no issue to expire against is durable prose", violations)
	}

	appendLine(t, path, "Skip the mtime check, awaiting #84.")
	violations, err = Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "self-expiring phrasing") {
		t.Fatalf("got %v, want an awaiting-#N violation", violations)
	}
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + line + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
