// Package skill installs and reconciles the bundled secondhand Agent Skill into a fleet
// home's supported per-harness project-local destinations, deterministically and without
// requiring symlink privilege: every copy is a plain file, atomically written.
package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/shellquote"
	hskills "github.com/atqamz/hand/skills"
)

// Name is the skill's directory name at every destination.
const Name = "secondhand"

// The one file every destination is guaranteed to have, so ownership and staleness checks
// have a fixed, cheap starting point rather than a directory listing.
const entryFile = "SKILL.md"

// The frontmatter field the canonical SKILL.md's metadata block carries. Presence as an actual
// frontmatter line, not merely a substring anywhere in the file, distinguishes a Hand-managed
// copy from a foreign file at the same destination; not a security boundary.
const managedMarker = "managed-by: hand"

// The frontmatter delimiter Agent Skill files use to bound their YAML metadata block.
const frontmatterDelimiter = "---"

// Reports whether content's frontmatter block - the span between its first two lines that are
// exactly "---" - contains managedMarker as one of its own lines, so a foreign file that merely
// mentions the marker in prose is never mistaken for a Hand-managed copy.
func isManaged(content string) bool {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return false
	}
	closeAt := -1
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == frontmatterDelimiter {
			closeAt = i
			break
		}
	}
	if closeAt < 0 {
		return false
	}
	for _, line := range lines[1 : 1+closeAt] {
		if strings.TrimSpace(line) == managedMarker {
			return true
		}
	}
	return false
}

type destination struct {
	rel string
}

// One relative root per supported harness, deduplicated where harnesses share a convention:
// Codex and OpenCode have no distinct Agent Skill directory of their own, so both read the
// generic .agents/skills root the same way every supported harness already reads AGENTS.md.
var destinations = []destination{
	{rel: filepath.Join(".claude", "skills")},
	{rel: filepath.Join(".grok", "skills")},
	{rel: filepath.Join(".pi", "skills")},
	{rel: filepath.Join(".agents", "skills")},
}

// Kept for tests and callers that want to reason about which harness a destination serves;
// production code only needs DestinationDirs.
var harnessRel = map[string]string{
	harness.Claude:   filepath.Join(".claude", "skills"),
	harness.Grok:     filepath.Join(".grok", "skills"),
	harness.Pi:       filepath.Join(".pi", "skills"),
	harness.Codex:    filepath.Join(".agents", "skills"),
	harness.OpenCode: filepath.Join(".agents", "skills"),
}

// DestinationDirs returns every supported harness's skill directory under home, one absolute
// path per distinct destination root.
func DestinationDirs(home string) []string {
	dirs := make([]string, 0, len(destinations))
	for _, d := range destinations {
		dirs = append(dirs, filepath.Join(home, d.rel, Name))
	}
	return dirs
}

// Outcome reports what Refresh did, or found, at one destination.
type Outcome string

const (
	OutcomeCreated   Outcome = "created"
	OutcomeRefreshed Outcome = "refreshed"
	OutcomeUnchanged Outcome = "unchanged"
	OutcomeConflict  Outcome = "conflict"
)

// DestinationResult reports Refresh's outcome at one destination directory.
type DestinationResult struct {
	Dir     string
	Outcome Outcome
}

// Refresh installs or reconciles the bundled skill into every supported destination under a
// fleet home, independently: create if missing, refresh if stale, refuse a foreign file as a
// conflict, or leave canonical content untouched. It returns nil if dir is not a fleet home.
func Refresh(dir string) ([]DestinationResult, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return nil, err
	}
	if !isHome {
		return nil, nil
	}

	files, err := canonicalFiles()
	if err != nil {
		return nil, err
	}

	results := make([]DestinationResult, 0, len(destinations))
	for _, destDir := range DestinationDirs(dir) {
		outcome, err := refreshOne(destDir, files)
		if err != nil {
			return nil, fmt.Errorf("refresh skill at %s: %w", destDir, err)
		}
		results = append(results, DestinationResult{Dir: destDir, Outcome: outcome})
	}
	return results, nil
}

func refreshOne(destDir string, files map[string][]byte) (Outcome, error) {
	entryPath := filepath.Join(destDir, entryFile)
	existing, err := os.ReadFile(entryPath)
	switch {
	case os.IsNotExist(err):
		occupied, err := destDirHasAnyEntry(destDir)
		if err != nil {
			return "", err
		}
		if occupied {
			// SKILL.md is absent, but something else already occupies this destination: a
			// partial or foreign install with no ownership marker to trust. Refuse rather
			// than let writeAll silently overwrite whatever is already there.
			return OutcomeConflict, nil
		}
		if err := writeAll(destDir, files); err != nil {
			return "", err
		}
		return OutcomeCreated, nil
	case err != nil:
		return "", fmt.Errorf("read %s: %w", entryPath, err)
	case !isManaged(string(existing)):
		return OutcomeConflict, nil
	}

	stale, err := isStale(destDir, files)
	if err != nil {
		return "", err
	}
	if !stale {
		return OutcomeUnchanged, nil
	}
	if err := writeAll(destDir, files); err != nil {
		return "", err
	}
	return OutcomeRefreshed, nil
}

// Reports whether destDir exists and already contains at least one entry, so a missing SKILL.md
// is trusted as "genuinely empty destination" only when nothing else backs that assumption up.
func destDirHasAnyEntry(destDir string) (bool, error) {
	entries, err := os.ReadDir(destDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", destDir, err)
	}
	return len(entries) > 0, nil
}

func isStale(destDir string, files map[string][]byte) (bool, error) {
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(destDir, rel))
		if os.IsNotExist(err) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("read %s: %w", filepath.Join(destDir, rel), err)
		}
		if string(got) != string(want) {
			return true, nil
		}
	}
	return false, nil
}

func writeAll(destDir string, files map[string][]byte) error {
	for rel, data := range files {
		path := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(rel), err)
		}
		if err := atomicfile.Write(path, ".secondhand-skill-", data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// Severity distinguishes a Violation that fails hand doctor from one that is informational.
type Severity int

const (
	SeverityViolation Severity = iota
	SeverityInfo
)

// Violation is one drift, missing-file, or conflict finding Check found at one destination.
type Violation struct {
	Dir      string
	Text     string
	Severity Severity
}

// Check reports whether every supported destination's bundled skill matches the canonical
// content, without fixing anything, the same report/restore split agentsmd.Check draws for
// AGENTS.md. A nil result with no error means dir is not a fleet home.
func Check(dir string) ([]Violation, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return nil, err
	}
	if !isHome {
		return nil, nil
	}

	files, err := canonicalFiles()
	if err != nil {
		return nil, err
	}

	var violations []Violation
	for _, destDir := range DestinationDirs(dir) {
		v, err := checkOne(dir, destDir, files)
		if err != nil {
			return nil, fmt.Errorf("check skill at %s: %w", destDir, err)
		}
		violations = append(violations, v...)
	}
	return violations, nil
}

func checkOne(fleetHome, destDir string, files map[string][]byte) ([]Violation, error) {
	entryPath := filepath.Join(destDir, entryFile)
	existing, err := os.ReadFile(entryPath)
	switch {
	case os.IsNotExist(err):
		occupied, occErr := destDirHasAnyEntry(destDir)
		if occErr != nil {
			return nil, occErr
		}
		if occupied {
			return []Violation{{Dir: destDir, Text: fmt.Sprintf("bundled skill at %s has unmanaged content with no SKILL.md to identify it: move it aside, then run hand init %s to install the skill there", destDir, shellquote.Quote(fleetHome))}}, nil
		}
		return []Violation{{Dir: destDir, Text: fmt.Sprintf("bundled skill is missing at %s: run hand init %s to install it", destDir, shellquote.Quote(fleetHome))}}, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", entryPath, err)
	case !isManaged(string(existing)):
		return []Violation{{Dir: destDir, Text: fmt.Sprintf("bundled skill at %s is a foreign, unmanaged file: move it aside, then run hand init %s to install the skill there", destDir, shellquote.Quote(fleetHome))}}, nil
	}

	stale, err := isStale(destDir, files)
	if err != nil {
		return nil, err
	}
	if !stale {
		return nil, nil
	}
	return []Violation{{Dir: destDir, Text: fmt.Sprintf("bundled skill at %s has drifted from the canonical content: run hand init %s to refresh it", destDir, shellquote.Quote(fleetHome))}}, nil
}

// Walks the embedded skill tree once into a flat relative-path -> content map, so every
// destination compares and writes against the exact same in-memory snapshot.
func canonicalFiles() (map[string][]byte, error) {
	root := Name
	files := make(map[string][]byte)
	err := fs.WalkDir(hskills.Secondhand, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(hskills.Secondhand, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.FromSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded skill tree: %w", err)
	}
	return files, nil
}
