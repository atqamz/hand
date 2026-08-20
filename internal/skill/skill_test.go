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
