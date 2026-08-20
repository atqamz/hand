package agentsmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withWindows(t *testing.T) {
	t.Helper()
	restore := isWindows
	isWindows = func() bool { return true }
	t.Cleanup(func() { isWindows = restore })
}

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
	result, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("got Changed=true, want false outside a fleet home")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("got AGENTS.md written outside a fleet home, err=%v", err)
	}
}

func TestRefreshWritesCanonicalAgentsMdAndClaudeReferenceWhenMissing(t *testing.T) {
	dir := makeWorkspace(t)

	result, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("got Changed=false, want true")
	}
	if result.ArchivedPath != "" {
		t.Fatalf("got ArchivedPath %q, want none for a fresh home with nothing to preserve", result.ArchivedPath)
	}

	got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != generatedBody {
		t.Fatalf("got %q, want the exact canonical body", got)
	}
	if !strings.Contains(string(got), "hand session start") || !strings.Contains(string(got), "HAND_ROLE=worker") {
		t.Fatalf("got %q, want the compact supervisor bootstrap", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "agents-md-legacy-migration.md")); !os.IsNotExist(err) {
		t.Fatal("got a legacy archive file for a fresh home, want none")
	}

	claudePath := filepath.Join(dir, "CLAUDE.md")
	if runtime.GOOS == "windows" {
		got, err := os.ReadFile(claudePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != windowsClaudeContent {
			t.Fatalf("got CLAUDE.md content %q, want %q", got, windowsClaudeContent)
		}
	} else {
		link, err := os.Readlink(claudePath)
		if err != nil {
			t.Fatal(err)
		}
		if link != "AGENTS.md" {
			t.Fatalf("got CLAUDE.md -> %q, want AGENTS.md", link)
		}
	}
}

func TestSupervisorInstructionsCoverDurableOperatingContract(t *testing.T) {
	got := strings.Join(SupervisorInstructions(), "\n")
	for _, want := range []string{
		"data/operator.md",
		"data/projects.md",
		"data/backlog.md",
		"data/<id>/brief.md",
		"state/<id>.status",
		"working:",
		"paused:",
		"blocked:",
		"needs-decision:",
		"done:",
		"failed:",
		"re-arm",
		"Never merge without explicit authorization",
		"hand deliver",
		"Never edit files under `projects/`",
		"full and absolute, never relative",
		"hand hold set",
		"hand hold clear",
		"data/done-archive.md",
		"data/note-archive.md",
		"data/learnings.md",
		"Only a `hand send` message carries an operator decision",
		"TOON",
		"Branch on `kind`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("got supervisor instructions %q, want durable rule %q", got, want)
		}
	}
}

func TestSupervisorInstructionsUseCanonicalTaskKinds(t *testing.T) {
	got := strings.Join(SupervisorInstructions(), "\n")
	for _, want := range []string{
		"`scout` and `ship` are Task kinds, not worker roles",
		"Use `scout` when the intended deliverable is an investigation/report",
		"`ship` when it is a change that must be landed or explicitly delivered",
		"A worker is the agent process Hand launches to execute delegated work",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("got supervisor instructions %q, want canonical task-kind rule %q", got, want)
		}
	}
}

func TestSupervisorInstructionsCoverExecutionPlanningContract(t *testing.T) {
	got := strings.Join(SupervisorInstructions(), "\n")
	for _, want := range []string{
		"execution class",
		"mechanical",
		"standard",
		"deep",
		"remaining executor judgment",
		"decision-complete",
		"planned_against",
		"verified default branch",
		"scout first",
		"routine classification",
		"operator-owned tradeoff",
		"not task size",
		"headings are recommendations",
		"re-check",
		"every new execution-class brief",
		"standard and deep retain it as provenance",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("got supervisor instructions %q, want durable execution-planning concept %q", got, want)
		}
	}
	if strings.Contains(got, "mechanical means small") || strings.Contains(got, "deep means large") {
		t.Fatalf("got supervisor instructions %q, want no task-size classification", got)
	}
}

func TestSupervisorInstructionsCoverProfileAndRouteConfiguration(t *testing.T) {
	got := strings.Join(SupervisorInstructions(), "\n")
	for _, want := range []string{
		"`hand config`",
		"supported harnesses",
		"installed on `PATH`",
		"Profile or Route is missing",
		"structural options",
		"entitlement, quality, or cost",
		"only unresolved operator policy",
		"routine confirmation",
		"Classify the task's execution class",
		"`hand config profile set <name> --harness <harness>`",
		"`hand config route set <kind> <execution-class> <profile>`",
		"omit `model` and `effort` front matter",
		"genuine task-specific need",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("got supervisor instructions %q, want profile-and-route configuration rule %q", got, want)
		}
	}
	for _, forbidden := range []string{"cheap", "expensive", "premium", "best model", "high-quality", "low-quality"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("got supervisor instructions %q, want no semantic harness or model label %q", got, forbidden)
		}
	}
}

func TestSupervisorInstructionsReturnsClone(t *testing.T) {
	first := SupervisorInstructions()
	first[0] = "changed by caller"
	if got := SupervisorInstructions()[0]; got == "changed by caller" {
		t.Fatal("SupervisorInstructions exposed mutable package state")
	}
}

// atqamz/hand#87's fix has to reach every worker without a new report-vocabulary word, since the
// watcher's classifier and hand status's renderer were outside that change's scope: the working: prefix
// plus a first-person convention was the only lever available.
func TestGeneratedRulesCoverSelfDecidedCallsInFirstPerson(t *testing.T) {
	instructions := strings.Join(SupervisorInstructions(), "\n")
	if !strings.Contains(instructions, "hand send") || !strings.Contains(instructions, "operator decision") {
		t.Fatalf("got instructions %q, want the hand send invariant", instructions)
	}
	if !strings.Contains(instructions, "working: deciding myself:") {
		t.Fatalf("got instructions %q, want the first-person working: convention", instructions)
	}
}

// atqamz/hand#114: a fleet agent following the template had no reason
// to reach for hand hold, since every "waiting on" case routed through
// data/backlog.md and hand send.
func TestGeneratedRulesCoverHolds(t *testing.T) {
	instructions := strings.Join(SupervisorInstructions(), "\n")
	if !strings.Contains(instructions, "hand hold set") || !strings.Contains(instructions, "hand hold clear") {
		t.Fatalf("got instructions %q, want hand hold set and hand hold clear", instructions)
	}
}

// atqamz/hand#47: the four files hand init seeds are inert unless the template says
// who reads each one and when. atqamz/hand#64: the one direction data/ does not carry
// has to be stated, or the agent invents a hand-written operator channel again.
func TestGeneratedRulesCoverOperatorContextLearningsAndArchives(t *testing.T) {
	instructions := strings.Join(SupervisorInstructions(), "\n")
	for _, want := range []string{
		"data/operator.md",
		"data/learnings.md",
		"data/done-archive.md",
		"data/note-archive.md",
		"written for the operator to read",
		"never rewrite it",
	} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("got instructions %q, want it to name %q", instructions, want)
		}
	}
	if strings.Contains(instructions, "data/inbox.md") {
		t.Fatalf("got instructions %q, want no hand-written operator channel", instructions)
	}
}

func TestAgentsMdBodyItselfCarriesNoBannedCharacters(t *testing.T) {
	if strings.ContainsRune(generatedBody, '—') {
		t.Fatal("got an em dash in the canonical AGENTS.md body, want none")
	}
	for _, r := range generatedBody {
		if r >= 0x1F300 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF {
			t.Fatalf("got emoji rune %q in the canonical AGENTS.md body, want none", r)
		}
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

func TestRefreshWritesWindowsClaudeFileWhenMissing(t *testing.T) {
	withWindows(t)
	dir := makeWorkspace(t)

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("got CLAUDE.md as a symlink, want a regular file on Windows")
	}
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != windowsClaudeContent {
		t.Fatalf("got CLAUDE.md content %q, want %q", got, windowsClaudeContent)
	}
}

func TestRefreshDoesNotOverwriteExistingWindowsClaudeFile(t *testing.T) {
	withWindows(t)
	dir := makeWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "custom\n" {
		t.Fatalf("got CLAUDE.md content %q, want unchanged %q", got, "custom\n")
	}
}

// An already-current AGENTS.md must not be rewritten at all: an identical-bytes write
// still swaps the inode, resets the mode, and turns a symlink into a regular file.
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

	result, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("got Changed=true, want false when the file is already canonical")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("got AGENTS.md replaced, want the existing file left untouched")
	}
	if runtime.GOOS != "windows" && after.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want the existing 0600 preserved", after.Mode().Perm())
	}
}

// CONTRIBUTING.md documents that a clean checkout's tracked AGENTS.md is already the exact
// content `hand init` would write, so bootstrapping the checkout as a dogfood fleet home is a
// no-op. That claim only holds if the tracked file matches generatedBody byte for byte.
func TestThisRepoAgentsMdIsCurrentForDogfood(t *testing.T) {
	repoCopy, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(repoCopy) != generatedBody {
		t.Fatalf("got repo AGENTS.md %q, want it to equal the current canonical body exactly", repoCopy)
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

func TestCheckSkipsNonFleetHome(t *testing.T) {
	violations, err := Check(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if violations != nil {
		t.Fatalf("got %v, want nil outside a fleet home", violations)
	}
}

func TestCheckFlagsMissingAgentsFile(t *testing.T) {
	dir := makeWorkspace(t)
	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !hasViolation(violations, "AGENTS.md is missing") {
		t.Fatalf("got %v, want one missing-file violation", violations)
	}
	if violations[0].Severity != SeverityViolation {
		t.Fatalf("got severity %v, want SeverityViolation", violations[0].Severity)
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

func TestCheckCleanForWindowsClaudeFileRightAfterRefresh(t *testing.T) {
	withWindows(t)
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %v, want no violations for a correct Windows CLAUDE.md", violations)
	}
}

func TestCheckFlagsMissingWindowsClaudeFile(t *testing.T) {
	withWindows(t)
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "CLAUDE.md is missing") {
		t.Fatalf("got %v, want a missing CLAUDE.md violation", violations)
	}
}

func TestCheckFlagsDriftedWindowsClaudeFile(t *testing.T) {
	withWindows(t)
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("something else\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "CLAUDE.md is not the Windows") {
		t.Fatalf("got %v, want a drifted CLAUDE.md violation", violations)
	}
}

func TestCheckFlagsDriftedAgentsMd(t *testing.T) {
	dir := makeWorkspace(t)
	if _, err := Refresh(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(content), "hand session start", "hand session begin", 1)
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "AGENTS.md has drifted from the canonical Hand-owned content") {
		t.Fatalf("got %v, want a drift violation", violations)
	}
	// A bare "run hand init" would target the operator's working directory,
	// which is a new nested fleet home whenever that is not the home itself.
	if !hasViolation(violations, "run hand init '"+dir+"' to restore it") {
		t.Fatalf("got %v, want the remedy to name the resolved home %q", violations, dir)
	}
}

func TestRefreshMigratesOldMixedOwnershipAgentsMdArchivingNonGeneratedContentVerbatim(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")

	userPreamble := "# House rules\n\nRead this before the generated block.\n\n"
	userTrailer := "\n- a project-specific rule the user wrote by hand\n\n## Maintaining this file\n\nKeep this file tidy.\n"
	stale := userPreamble + legacyBeginMarker + "\n# Secondhand\n\nAn out-of-date template.\n" + legacyEndMarker + userTrailer
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("got Changed=false, want true when migrating an old mixed-ownership file")
	}
	archivePath := filepath.Join(dir, "data", "agents-md-legacy-migration.md")
	if result.ArchivedPath != archivePath {
		t.Fatalf("got ArchivedPath %q, want %q", result.ArchivedPath, archivePath)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != generatedBody {
		t.Fatalf("got %q, want the exact canonical body replacing the mixed-ownership file", got)
	}

	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archived), userPreamble) {
		t.Fatalf("got archive %q, want the preamble preserved verbatim", archived)
	}
	if !strings.Contains(string(archived), userTrailer) {
		t.Fatalf("got archive %q, want the trailer preserved verbatim", archived)
	}
	if strings.Contains(string(archived), "An out-of-date template.") {
		t.Fatalf("got archive %q, want the old generated span itself dropped, not archived", archived)
	}
}

func TestRefreshMigratesForeignAgentsMdWithNoMarkersArchivingWholeFile(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")
	original := "# Project rules\n\nKeep this byte-for-byte.\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("got Changed=false, want true")
	}
	archivePath := filepath.Join(dir, "data", "agents-md-legacy-migration.md")
	if result.ArchivedPath != archivePath {
		t.Fatalf("got ArchivedPath %q, want %q", result.ArchivedPath, archivePath)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != generatedBody {
		t.Fatalf("got %q, want the exact canonical body", got)
	}
	archived, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archived), original) {
		t.Fatalf("got archive %q, want the whole foreign file preserved verbatim", archived)
	}
}

func TestRefreshMigrationIsIdempotentAndSubsequentDriftIsPlainRestoreWithNoReArchive(t *testing.T) {
	dir := makeWorkspace(t)
	path := filepath.Join(dir, "AGENTS.md")
	original := legacyBeginMarker + "\nstale\n" + legacyEndMarker + "\nuser content\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.ArchivedPath == "" {
		t.Fatal("got no archive on first migration, want one")
	}
	archivePath := first.ArchivedPath
	archivedFirst, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.ArchivedPath != "" {
		t.Fatalf("got %+v, want a no-op immediately after migration", second)
	}

	if err := os.WriteFile(path, []byte("operator hand-edited this file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := Refresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Changed {
		t.Fatal("got Changed=false, want the post-migration hand edit restored")
	}
	if third.ArchivedPath != "" {
		t.Fatalf("got ArchivedPath %q, want no re-archive for drift after migration is already done", third.ArchivedPath)
	}
	archivedAfter, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(archivedAfter) != string(archivedFirst) {
		t.Fatal("got the original migration archive overwritten, want it left alone")
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != generatedBody {
		t.Fatalf("got %q, want the canonical body restored", restored)
	}
}

func TestRefreshFailsSafeOnAmbiguousLegacyMarkersWithoutWritingOrArchiving(t *testing.T) {
	for name, content := range map[string]string{
		"missing end": legacyBeginMarker + "\nmissing end\n",
		"end only":    legacyEndMarker + "\n",
		"reversed":    legacyEndMarker + "\n" + legacyBeginMarker + "\n",
		"duplicate":   legacyBeginMarker + "\na\n" + legacyEndMarker + "\n" + legacyBeginMarker + "\nb\n" + legacyEndMarker + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := makeWorkspace(t)
			path := filepath.Join(dir, "AGENTS.md")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Refresh(dir); err == nil {
				t.Fatal("Refresh succeeded, want an ambiguous-marker error")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Fatalf("got %q, want unchanged %q", got, content)
			}
			if _, err := os.Stat(filepath.Join(dir, "data", "agents-md-legacy-migration.md")); !os.IsNotExist(err) {
				t.Fatal("got a legacy archive written for an ambiguous file, want none")
			}
			if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
				t.Fatalf("got CLAUDE.md change after an ambiguous-marker failure, err=%v", err)
			}
		})
	}
}
