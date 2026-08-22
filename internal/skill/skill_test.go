package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
)

func makeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "hand.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func outcomeFor(results []DestinationResult, dir string) (Outcome, bool) {
	for _, r := range results {
		if r.Dir == dir {
			return r.Outcome, true
		}
	}
	return "", false
}

func TestRefreshSkipsSilentlyWhenNotAFleetHome(t *testing.T) {
	results, err := Refresh(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Fatalf("got %v, want nil outside a fleet home", results)
	}
}

func TestDestinationDirsCoverEverySupportedHarnessWithNoDuplicates(t *testing.T) {
	home := t.TempDir()
	dirs := DestinationDirs(home)
	seen := make(map[string]bool)
	for _, d := range dirs {
		if seen[d] {
			t.Fatalf("got duplicate destination %q in %v", d, dirs)
		}
		seen[d] = true
	}
	for _, h := range harness.Names() {
		if !harness.SupportsSupervisor(h) {
			continue
		}
		rel, ok := harnessRel[h]
		if !ok {
			t.Fatalf("harness %q has no mapped skill destination", h)
		}
		want := filepath.Join(home, rel, Name)
		if !seen[want] {
			t.Fatalf("got %v, want it to include %q for harness %q", dirs, want, h)
		}
	}
}

func TestRefreshCreatesEveryDestinationWhenMissing(t *testing.T) {
	home := makeHome(t)
	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	dirs := DestinationDirs(home)
	if len(results) != len(dirs) {
		t.Fatalf("got %d results, want %d", len(results), len(dirs))
	}
	for _, dir := range dirs {
		outcome, ok := outcomeFor(results, dir)
		if !ok || outcome != OutcomeCreated {
			t.Fatalf("got outcome %q ok=%v for %s, want created", outcome, ok, dir)
		}
		skillMD, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if len(skillMD) == 0 {
			t.Fatalf("got empty SKILL.md at %s", dir)
		}
		for _, ref := range []string{
			"setup-doctor.md", "configuration.md", "planning-and-briefs.md",
			"task-lifecycle.md", "supervision-loop.md", "evaluation.md",
			"recovery.md", "bug-report.md",
		} {
			if _, err := os.Stat(filepath.Join(dir, "references", ref)); err != nil {
				t.Fatalf("got missing reference %s at %s: %v", ref, dir, err)
			}
		}
	}
}

func TestRefreshIsUnchangedWhenAlreadyCanonical(t *testing.T) {
	home := makeHome(t)
	if _, err := Refresh(home); err != nil {
		t.Fatal(err)
	}
	dir := DestinationDirs(home)[0]
	path := filepath.Join(dir, "SKILL.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Outcome != OutcomeUnchanged {
			t.Fatalf("got outcome %q for %s on a second run, want unchanged", r.Outcome, r.Dir)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("got SKILL.md replaced on an unchanged refresh, want the existing file left untouched")
	}
}

func TestRefreshAtomicallyRefreshesAStaleManagedCopy(t *testing.T) {
	home := makeHome(t)
	if _, err := Refresh(home); err != nil {
		t.Fatal(err)
	}
	dir := DestinationDirs(home)[0]
	path := filepath.Join(dir, "SKILL.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	drifted := string(original) + "\nstray trailing line\n"
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := outcomeFor(results, dir)
	if !ok || outcome != OutcomeRefreshed {
		t.Fatalf("got outcome %q ok=%v, want refreshed", outcome, ok)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("got %q, want the stale copy restored to the canonical body", got)
	}
}

func TestRefreshRefusesAForeignFileWithoutOverwritingIt(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "# Some unrelated skill\n\nNothing to do with hand.\n"
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := outcomeFor(results, dir)
	if !ok || outcome != OutcomeConflict {
		t.Fatalf("got outcome %q ok=%v, want conflict", outcome, ok)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != foreign {
		t.Fatalf("got %q, want the foreign file left exactly as written", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "references")); !os.IsNotExist(err) {
		t.Fatal("got a references directory written alongside a refused foreign file, want none")
	}

	// A conflict at one destination must not block installing the others.
	for _, other := range DestinationDirs(home)[1:] {
		otherOutcome, ok := outcomeFor(results, other)
		if !ok || otherOutcome != OutcomeCreated {
			t.Fatalf("got outcome %q ok=%v for unaffected destination %s, want created", otherOutcome, ok, other)
		}
	}
}

// A destination can be missing SKILL.md while still holding other files: a partial install left
// over from a prior failed write, or content a user created without ever adding a SKILL.md.
// Refresh must not treat "no SKILL.md" as "safe to write" in that case.
func TestRefreshRefusesToWriteOverUnmarkedContentWhenSkillMdIsMissing(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "# Someone else's notes\n"
	foreignPath := filepath.Join(dir, "references", "recovery.md")
	if err := os.WriteFile(foreignPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := outcomeFor(results, dir)
	if !ok || outcome != OutcomeConflict {
		t.Fatalf("got outcome %q ok=%v, want conflict when unmarked content already occupies the destination", outcome, ok)
	}
	got, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != foreign {
		t.Fatalf("got %q, want the pre-existing file left exactly as written", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("got SKILL.md written into an occupied, unmarked destination, err=%v", err)
	}
}

func TestCheckFlagsUnmarkedContentAtAMissingDestinationAsAConflict(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(home)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "unmanaged content with no SKILL.md") {
		t.Fatalf("got %v, want an unmanaged-content violation for %s", violations, dir)
	}
	if hasViolation(violations, "bundled skill is missing at "+dir+":") {
		t.Fatalf("got %v, want the occupied-destination wording, not the plain missing wording", violations)
	}
}

// The marker must be an actual frontmatter line, not merely a substring anywhere in the file:
// a foreign skill that happens to mention it in prose must still be refused as a conflict.
func TestRefreshTreatsAMarkerMentionedOutsideFrontmatterAsForeign(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "# Someone else's skill\n\nThis document mentions managed-by: hand in passing, not as frontmatter.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := outcomeFor(results, dir)
	if !ok || outcome != OutcomeConflict {
		t.Fatalf("got outcome %q ok=%v, want conflict for a marker mentioned outside frontmatter", outcome, ok)
	}
	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != foreign {
		t.Fatalf("got %q, want the foreign file left exactly as written", got)
	}
}

// The marker only counts inside a properly closed frontmatter block: an unclosed "---" must not
// let the rest of the file be scanned as if it were still frontmatter.
func TestRefreshTreatsAnUnclosedFrontmatterAsForeignEvenIfTheMarkerAppears(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "---\nmanaged-by: hand\n\n# The frontmatter above never closes.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Refresh(home)
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := outcomeFor(results, dir)
	if !ok || outcome != OutcomeConflict {
		t.Fatalf("got outcome %q ok=%v, want conflict for an unclosed frontmatter block", outcome, ok)
	}
	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != foreign {
		t.Fatalf("got %q, want the foreign file left exactly as written", got)
	}
}

func TestRefreshEmbeddedSkillCarriesTheManagedMarkerAndNoBannedCharacters(t *testing.T) {
	home := makeHome(t)
	if _, err := Refresh(home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(DestinationDirs(home)[0], "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, managedMarker) {
		t.Fatalf("got SKILL.md %q, want the managed-by marker", content)
	}
	if !strings.Contains(content, "source: atqamz/hand") {
		t.Fatalf("got SKILL.md %q, want a source marker naming the project", content)
	}
	if strings.ContainsRune(content, '—') {
		t.Fatal("got an em dash in the canonical SKILL.md, want none")
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

func TestCheckFlagsMissingDestinationsBeforeAnyRefresh(t *testing.T) {
	home := makeHome(t)
	violations, err := Check(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != len(DestinationDirs(home)) {
		t.Fatalf("got %d violations, want one per destination: %v", len(violations), violations)
	}
	if !hasViolation(violations, "bundled skill is missing") {
		t.Fatalf("got %v, want a missing-skill violation", violations)
	}
}

func TestCheckCleanRightAfterRefresh(t *testing.T) {
	home := makeHome(t)
	if _, err := Refresh(home); err != nil {
		t.Fatal(err)
	}
	violations, err := Check(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %v, want no violations right after Refresh", violations)
	}
}

func TestCheckFlagsDriftedSkillContent(t *testing.T) {
	home := makeHome(t)
	if _, err := Refresh(home); err != nil {
		t.Fatal(err)
	}
	dir := DestinationDirs(home)[0]
	path := filepath.Join(dir, "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("\nstray line\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Dir != dir {
		t.Fatalf("got %v, want exactly one violation naming %s", violations, dir)
	}
	if !hasViolation(violations, "has drifted from the canonical content") {
		t.Fatalf("got %v, want a drift violation", violations)
	}
	if !hasViolation(violations, "run hand init '"+home+"'") {
		t.Fatalf("got %v, want the remedy to name the resolved home", violations)
	}
}

func TestCheckFlagsAForeignFileAsAConflict(t *testing.T) {
	home := makeHome(t)
	dir := DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := Check(home)
	if err != nil {
		t.Fatal(err)
	}
	if !hasViolation(violations, "foreign, unmanaged file") {
		t.Fatalf("got %v, want a foreign-file violation for %s", violations, dir)
	}
	// The other destinations are still genuinely missing, distinct from this conflict.
	if n := len(violations); n != len(DestinationDirs(home)) {
		t.Fatalf("got %d violations, want one per destination (one conflict, the rest missing): %v", n, violations)
	}
}
