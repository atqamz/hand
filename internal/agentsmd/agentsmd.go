// Package agentsmd generates and restores the AGENTS.md supervisor contract that hand init
// writes into a fleet home, and checks an existing one for drift from that canonical content.
package agentsmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/shellquote"
)

const (
	filename    = "AGENTS.md"
	symlinkName = "CLAUDE.md"

	// The previous mixed-ownership format wrapped hand's generated content in this marked
	// span. Migrate still recognizes it to archive whatever a fleet home kept outside it;
	// nothing else here writes these markers, since the canonical file is generated end to end.
	legacyBeginMarker = "<!-- hand:generated:start -->"
	legacyEndMarker   = "<!-- hand:generated:end -->"

	legacyArchiveRel = "data/agents-md-legacy-migration.md"

	// Creating a symlink on Windows needs a privilege hand cannot assume is present, so
	// Windows gets a regular file that points at AGENTS.md by content instead of by inode.
	windowsClaudeContent = "@AGENTS.md\n"
)

// Overridable so both the symlink and the Windows regular-file paths through
// Refresh and Check get real test coverage on every platform that runs the suite.
var isWindows = func() bool {
	return runtime.GOOS == "windows"
}

// OperatorDecisionRule is the one supervisor rule a worker needs wherever it runs, and a
// worktree is never under the fleet home, so it never loads the home's AGENTS.md.
// internal/harness puts this string in the launch prompt and the session contract includes it.
const OperatorDecisionRule = "Only a `hand send` message carries an operator decision. " +
	"Answering your own harness's question dialog is you deciding, not the operator - " +
	"never record that answer as \"operator said\" or \"operator chose\". " +
	"You may decide anything reversible yourself and proceed; say so in first person - " +
	"`working: deciding myself: <the call> because <reason>` - " +
	"and reserve `needs-decision:` for what you cannot take back."

// The entire canonical AGENTS.md content: a small, stable set of invariants, never detailed
// procedure. hand init restores it byte-for-byte; detailed procedure lives in the bundled
// secondhand Agent Skill, and living context lives under data/.
const generatedBody = `## Secondhand supervisor bootstrap

Before responding or acting in a supervising session, run ` + "`hand session start`" + `.
Do not run supervisor bootstrap when ` + "`HAND_ROLE=worker`" + `.

This file is Hand-owned and immutable: ` + "`hand init`" + ` restores it byte-for-byte, and
nobody edits it by hand, including the supervisor. The same rule covers every other
Hand-generated surface in this fleet home.

- Read ` + "`data/operator.md`" + ` before acting; its constraints outrank your own judgment.
- ` + "`data/**`" + ` is living fleet context and memory, never part of this file.
- Use the bundled ` + "`secondhand`" + ` Agent Skill for setup, routing, planning, task
  lifecycle, recovery, and bug-report procedures; this file states invariants, not procedures.
- Use the ` + "`hand`" + ` CLI and runtime as the source of truth for fleet and machine state
  instead of reading or editing it directly.
- Never edit a registered project under ` + "`projects/`" + ` directly; a worker does that in
  its own worktree.
- Never merge without explicit operator authorization.
`

var supervisorInstructions = []string{
	"Read `data/operator.md` before anything else. Its constraints outrank your own judgment.",
	"Match the request to a project in `data/projects.md`.",
	"`scout` and `ship` are Task kinds, not worker roles. Use `scout` when the intended deliverable is an investigation/report and `ship` when it is a change that must be landed or explicitly delivered. A worker is the agent process Hand launches to execute delegated work.",
	"The `execution class` means how much remaining executor judgment exists after planning, not task size, line count, or file count. Use `mechanical` only for a decision-complete plan where the executor applies specified changes and verifies; use `standard` when architecture is decided but ordinary reversible implementation judgment remains; use `deep` when substantial implementation reasoning remains. Execution classes are not worker roles or model/cost tiers.",
	"Investigate enough before dispatch to make implementation decisions explicit. Use a scout first when investigation is the cleanest way to remove uncertainty.",
	"Classify routine work yourself and do not ask the operator for routine classification. Ask only when a genuinely operator-owned tradeoff remains.",
	"Run `hand config` before proposing or configuring execution Profiles and Routes. Its `harnesses` table lists supported harnesses, while the `installed` column separately reports whether each is installed on `PATH`.",
	"Classify the task's execution class before selecting a route. When a Profile or Route is missing, propose structural options from the task kind and execution class without entitlement, quality, or cost claims about harnesses, models, profiles, or routes.",
	"Ask only unresolved operator policy questions, not routine confirmation. Persist an accepted structure with `hand config profile set <name> --harness <harness>` and `hand config route set <kind> <execution-class> <profile>`, then inspect `hand config` again.",
	"Normally omit `model` and `effort` front matter from briefs. Use explicit overrides only for a genuine task-specific need, not routine routing.",
	"A mechanical ship brief should include the goal, verified current state, locked decisions, exact files/packages/symbols, ordered implementation steps, invariants, tests, verification, non-goals, and stop/escalate conditions. Recommended headings are guidance only; headings are recommendations, not syntax Hand parses.",
	"Immediately before finalizing every new execution-class brief, resolve the registered project's verified default branch in `<home>/projects/<project>`, investigate against that revision, and record its full commit ID as `planned_against`. Mechanical dispatch compares the same local base later and refuses drift; standard and deep retain it as provenance without the mechanical exact-match refusal. If the project advances, re-check assumptions and rewrite or revalidate the plan rather than merely updating the SHA.",
	"Edit `data/backlog.md` to record the task with a unique ID.",
	"Write a brief at `data/<id>/brief.md`, including the absolute path to `state/<id>.status` and the report vocabulary the worker should append to it.",
	"`hand status <id>` shows a worker's reported state. Workers report with `working:`, `paused:`, `blocked:`, `needs-decision:`, `done:`, or `failed:`.",
	"Run `hand watch --until-event` as a background task to monitor the fleet. Arming observes the fleet's already-actionable state first, so a condition that arrived while nothing was watching still wakes it; after that it exits on the first fleet event. Exit 8 means interruption and exit 9 means takeover replacement, neither of which is a fleet event. A supervisor should re-arm it after acting on an event or when intentionally resuming monitoring.",
	"Never merge without explicit authorization.",
	"Run `hand teardown <id>` after work is landed. Work that is handed off but whose landing is someone else's call is recorded with `hand deliver <id> --reason <text>` first.",
	"Never edit files under `projects/`. Workers do that in worktrees.",
	"Never force-teardown without explicit authorization. `--force` is for work nobody delivered; `hand deliver` is the answer for work that is delivered and not landed.",
	"Name a path in a brief, a status report, or an operator message: full and absolute, never relative.",
	"Waiting on the operator or on another task: `hand hold set <id> --kind operator --reason <text>` or `--kind blocked --reason <text> --blocked-on <id>`, and `hand hold clear <id>` once resolved.",
	"`data/operator.md` is the operator's file, not yours. Read it, never rewrite it: one-way ownership is what lets its constraints outrank your judgment.",
	"`data/backlog.md` is your task queue. Edit it directly.",
	"Roll finished backlog entries into `data/done-archive.md`, and dropped or superseded ones into `data/note-archive.md` with the reason they were dropped. Roll off rather than delete.",
	"`data/learnings.md` holds dated, evidence-backed operational facts. Read it when a task touches something it covers, add to it when a discovery cost real time, and curate it - rewrite and prune rather than append forever.",
	OperatorDecisionRule,
	"Every command prints TOON on stdout: `key: value` lines, `name[N]{f1,f2}:` blocks with one comma-joined row per line, and a `help[N]:` list. A count of `0` and an empty block are an answer, not a failure.",
	"A failure always writes one document to stderr: `error`, `kind`, and `exit`. Branch on `kind` rather than matching the message text. A command that already produced output keeps it on stdout.",
	"Nothing under `data/` is written for the operator to read. Report to them in the session.",
}

// Returns an isolated copy of the durable operating contract.
func SupervisorInstructions() []string {
	return append([]string(nil), supervisorInstructions...)
}

// Introduces the verbatim content migrate archived, so a human finds context
// without hand having guessed what any of it meant.
const legacyArchiveHeader = `# Archived AGENTS.md content

hand init migrated this fleet home's AGENTS.md to Hand's fully owned, immutable format.
Everything below is the non-generated content this home's AGENTS.md carried before that
migration ran, preserved exactly as written. Nothing here was reclassified: move anything
still useful into data/operator.md or data/learnings.md yourself.

---

`

// RefreshResult reports what Refresh did to a fleet home's AGENTS.md.
type RefreshResult struct {
	// Changed is true when Refresh wrote a new AGENTS.md, whether that was the first write,
	// a migration, or a restore over drifted or hand-edited content.
	Changed bool
	// ArchivedPath is the absolute path Migrate wrote pre-migration content to, or empty
	// when there was nothing to archive (a fresh home, or a home already migrated).
	ArchivedPath string
}

// Refresh writes or restores dir/AGENTS.md and its CLAUDE.md reference to the canonical
// Hand-owned content, archiving a pre-immutable file's non-generated content verbatim the
// first time it finds one. It returns a zero RefreshResult, nil if dir is not a fleet home.
func Refresh(dir string) (RefreshResult, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return RefreshResult{}, err
	}
	if !isHome {
		return RefreshResult{}, nil
	}

	path := filepath.Join(dir, filename)
	existing, err := os.ReadFile(path)
	canonical := []byte(generatedBody)

	var result RefreshResult
	switch {
	case os.IsNotExist(err):
		if err := atomicfile.Write(path, ".agents.md-", canonical, 0o644); err != nil {
			return RefreshResult{}, fmt.Errorf("write %s: %w", filename, err)
		}
		result.Changed = true
	case err != nil:
		return RefreshResult{}, fmt.Errorf("read %s: %w", filename, err)
	case string(existing) == generatedBody:
		// Already canonical: leave the file untouched rather than write identical bytes,
		// which would still swap the inode and reset the mode.
	default:
		archived, migrateErr := migrate(dir, string(existing))
		if migrateErr != nil {
			return RefreshResult{}, migrateErr
		}
		if err := atomicfile.Write(path, ".agents.md-", canonical, 0o644); err != nil {
			return RefreshResult{}, fmt.Errorf("write %s: %w", filename, err)
		}
		result.Changed = true
		result.ArchivedPath = archived
	}

	symlinkPath := filepath.Join(dir, symlinkName)
	if isWindows() {
		if err := writeWindowsClaudeFile(symlinkPath); err != nil {
			return RefreshResult{}, err
		}
		return result, nil
	}
	if _, err := os.Lstat(symlinkPath); os.IsNotExist(err) {
		if err := os.Symlink(filename, symlinkPath); err != nil {
			return RefreshResult{}, fmt.Errorf("create %s symlink: %w", symlinkName, err)
		}
	} else if err != nil {
		return RefreshResult{}, fmt.Errorf("check %s: %w", symlinkName, err)
	}

	return result, nil
}

// Archives existing's non-generated content the first time Refresh finds a fleet home not
// yet on the immutable model, identified by legacyArchiveRel being absent; once that one-time
// migration is done, drift in an immutable file is a plain restore, not a migration.
func migrate(dir, existing string) (string, error) {
	archivePath := filepath.Join(dir, legacyArchiveRel)
	switch _, err := os.Stat(archivePath); {
	case err == nil:
		return "", nil
	case !os.IsNotExist(err):
		return "", fmt.Errorf("check %s: %w", legacyArchiveRel, err)
	}

	legacy, err := legacyContent(existing)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(legacy) == "" {
		return "", nil
	}

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(legacyArchiveRel), err)
	}
	if err := atomicfile.Write(archivePath, ".agents-md-legacy-", []byte(legacyArchiveHeader+legacy), 0o644); err != nil {
		return "", fmt.Errorf("archive legacy AGENTS.md content: %w", err)
	}
	return archivePath, nil
}

// Extracts what a pre-immutable AGENTS.md carried outside hand's old marked span, verbatim,
// whether that span was well-formed, entirely absent, or ambiguous. An ambiguous span fails
// safely: hand does not guess which occurrence was real, so it errors rather than risk loss.
func legacyContent(content string) (string, error) {
	startCount := strings.Count(content, legacyBeginMarker)
	endCount := strings.Count(content, legacyEndMarker)
	switch {
	case startCount == 0 && endCount == 0:
		return content, nil
	case startCount != 1 || endCount != 1:
		return "", fmt.Errorf("AGENTS.md has malformed or duplicate hand:generated markers; resolve them by hand before hand init can migrate this file")
	}
	start := strings.Index(content, legacyBeginMarker)
	relEnd := strings.Index(content[start:], legacyEndMarker)
	if relEnd == -1 {
		return "", fmt.Errorf("AGENTS.md has malformed hand:generated markers; resolve them by hand before hand init can migrate this file")
	}
	end := start + relEnd + len(legacyEndMarker)
	return content[:start] + content[end:], nil
}

// A CLAUDE.md that already exists, in any form, is left alone: the same rule
// Refresh applies to an existing CLAUDE.md symlink on every other platform.
func writeWindowsClaudeFile(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check %s: %w", symlinkName, err)
	}
	if err := atomicfile.Write(path, ".claude.md-", []byte(windowsClaudeContent), 0o644); err != nil {
		return fmt.Errorf("create %s: %w", symlinkName, err)
	}
	return nil
}

// Severity distinguishes a Violation that fails hand doctor from one that is
// informational: real and worth a human's attention, but not something the checker can
// resolve into a pass/fail verdict on its own.
type Severity int

const (
	SeverityViolation Severity = iota
	SeverityInfo
)

// Violation is one drifted-content or missing-file hit Check found, at SeverityViolation
// unless Severity says otherwise.
type Violation struct {
	Line     int
	Text     string
	Severity Severity
}

// Check reports whether dir's AGENTS.md and its CLAUDE.md reference match the canonical
// Hand-owned content, without fixing either. A nil result with no error means the directory
// is not a fleet home; since the entire file is generated, any difference at all is drift.
func Check(dir string) ([]Violation, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return nil, err
	}
	if !isHome {
		return nil, nil
	}

	data, err := os.ReadFile(filepath.Join(dir, filename))
	if os.IsNotExist(err) {
		return []Violation{{Text: fmt.Sprintf("AGENTS.md is missing: run hand init %s to restore the canonical Hand-owned content", shellquote.Quote(dir))}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filename, err)
	}

	var violations []Violation
	if string(data) != generatedBody {
		violations = append(violations, Violation{Text: fmt.Sprintf("AGENTS.md has drifted from the canonical Hand-owned content: run hand init %s to restore it", shellquote.Quote(dir))})
	}

	if isWindows() {
		violations = append(violations, checkWindowsClaudeFile(dir)...)
	}

	return violations, nil
}

// Unix's CLAUDE.md is a symlink the filesystem itself keeps honest, so Check has never
// had to look at it; Windows's copy is ordinary file content that can drift or go missing.
func checkWindowsClaudeFile(dir string) []Violation {
	path := filepath.Join(dir, symlinkName)
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return []Violation{{Text: fmt.Sprintf("CLAUDE.md is missing: run hand init %s to restore it", shellquote.Quote(dir))}}
	case err != nil:
		return []Violation{{Text: fmt.Sprintf("read %s: %v", symlinkName, err)}}
	case string(data) != windowsClaudeContent:
		return []Violation{{Text: fmt.Sprintf("CLAUDE.md is not the Windows @AGENTS.md pointer: run hand init %s to restore it", shellquote.Quote(dir))}}
	}
	return nil
}
