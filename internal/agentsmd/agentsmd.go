// Package agentsmd generates and restores the AGENTS.md supervisor contract that hand init
// writes into a fleet home, and checks an existing one for drift from that canonical content.
package agentsmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/shellquote"
)

const (
	filename  = "AGENTS.md"
	claudeRef = "CLAUDE.md"

	// The previous mixed-ownership format wrapped hand's generated content in this marked
	// span. Migrate still recognizes it to archive whatever a fleet home kept outside it;
	// nothing else here writes these markers, since the canonical file is generated end to end.
	legacyBeginMarker = "<!-- hand:generated:start -->"
	legacyEndMarker   = "<!-- hand:generated:end -->"

	legacyArchiveRel = "data/agents-md-legacy-migration.md"

	// CLAUDE.md is a regular file pointing at AGENTS.md by content on every platform:
	// one shape everywhere, no symlink-creation privilege on Windows, and nothing for
	// tooling that stats or copies homes to special-case.
	claudeContent = "@AGENTS.md\n"
)

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

At the beginning of a new Supervisor runtime/session, run ` + "`hand session start`" + ` once.
Before reasoning or acting in every Supervisor turn, run ` + "`hand orient`" + `.
After an automatic wake/re-entry, run ` + "`hand orient`" + ` before any action.
Do not run supervisor commands when ` + "`HAND_ROLE=worker`" + `.

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
	"`unannounced` on a status row means no watcher has told anyone about a terminal report yet; `unacknowledged` means you have not. Run `hand ack <id> [--reason <text>]` once you have acted on it - reading `hand status` never clears it on its own.",
	"Run `hand orient` as the first Hand read of every reasoning turn, including a turn an automatic wake re-enters: it reports the bounded current orientation and records disposable mechanism progress only.",
	"Unattended wake delivery is host-specific: a supported host bridge arms `hand supervision wait --host <harness>`, which blocks until current actionable work is wake-eligible, emits one coalesced wake hint that becomes your next reasoning opportunity, and re-arms without model memory after each wake. A watcher process exit alone is delivery only where an owning host guarantees that conversion; never treat an exit code, a notification, or an accepted request as proof you reasoned.",
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

const noLegacyContentNote = "No pre-immutable AGENTS.md content needed preserving.\n"

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
		if err := ensureMigrationSentinel(dir); err != nil {
			return RefreshResult{}, err
		}
		if err := atomicfile.Write(path, ".agents.md-", canonical, 0o644); err != nil {
			return RefreshResult{}, fmt.Errorf("write %s: %w", filename, err)
		}
		result.Changed = true
	case err != nil:
		return RefreshResult{}, fmt.Errorf("read %s: %w", filename, err)
	case string(existing) == generatedBody:
		if err := ensureMigrationSentinel(dir); err != nil {
			return RefreshResult{}, err
		}
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

	claudePath := filepath.Join(dir, claudeRef)
	info, err := os.Lstat(claudePath)
	switch {
	case os.IsNotExist(err):
		if err := writeClaudePointer(claudePath); err != nil {
			return RefreshResult{}, err
		}
	case err != nil:
		return RefreshResult{}, fmt.Errorf("check %s: %w", claudeRef, err)
	case info.Mode()&os.ModeSymlink != 0:
		// A Hand-era symlink to AGENTS.md upgrades to the pointer file every
		// platform now uses; any other target is foreign and stays untouched.
		target, linkErr := os.Readlink(claudePath)
		if linkErr != nil {
			return RefreshResult{}, fmt.Errorf("read %s symlink: %w", claudeRef, linkErr)
		}
		if target == filename {
			if err := writeClaudePointer(claudePath); err != nil {
				return RefreshResult{}, err
			}
		}
	default:
		// An existing regular CLAUDE.md the operator owns stays untouched; only
		// drift from the canonical pointer content is a Check violation.
	}

	return result, nil
}

// Archives existing's non-generated content the first time Refresh finds a fleet home not
// yet on the immutable model, identified by legacyArchiveRel being absent; once that one-time
// migration is done, drift in an immutable file is a plain restore, not a migration.
func migrate(dir, existing string) (string, error) {
	archivePath := filepath.Join(dir, legacyArchiveRel)
	archive, err := os.ReadFile(archivePath)
	switch {
	case err == nil:
		if !strings.HasPrefix(string(archive), legacyArchiveHeader) {
			return "", fmt.Errorf("refuse to replace AGENTS.md: %s exists but is not Hand's migration archive", legacyArchiveRel)
		}
		return "", nil
	case !os.IsNotExist(err):
		return "", fmt.Errorf("check %s: %w", legacyArchiveRel, err)
	}

	legacy, err := legacyContent(existing)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(legacy) == "" {
		if err := writeMigrationSentinel(archivePath, noLegacyContentNote); err != nil {
			return "", err
		}
		return "", nil
	}

	if err := writeMigrationSentinel(archivePath, legacy); err != nil {
		return "", fmt.Errorf("archive legacy AGENTS.md content: %w", err)
	}
	return archivePath, nil
}

func ensureMigrationSentinel(dir string) error {
	archivePath := filepath.Join(dir, legacyArchiveRel)
	archive, err := os.ReadFile(archivePath)
	if err == nil {
		if !strings.HasPrefix(string(archive), legacyArchiveHeader) {
			return fmt.Errorf("refuse to replace AGENTS.md: %s exists but is not Hand's migration archive", legacyArchiveRel)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("check %s: %w", legacyArchiveRel, err)
	}
	return writeMigrationSentinel(archivePath, noLegacyContentNote)
}

func writeMigrationSentinel(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(legacyArchiveRel), err)
	}
	if err := atomicfile.Write(path, ".agents-md-legacy-", []byte(legacyArchiveHeader+body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", legacyArchiveRel, err)
	}
	return nil
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

// Creates the CLAUDE.md pointer file. Refresh resolves any pre-existing entry
// before calling this, so it only ever writes when the name is free.
func writeClaudePointer(path string) error {
	if err := atomicfile.Write(path, ".claude.md-", []byte(claudeContent), 0o644); err != nil {
		return fmt.Errorf("create %s: %w", claudeRef, err)
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

	violations = append(violations, checkClaudeFile(dir)...)

	return violations, nil
}

// CLAUDE.md is ordinary file content on every platform now, so Check reads and
// compares it everywhere the same way.
func checkClaudeFile(dir string) []Violation {
	path := filepath.Join(dir, claudeRef)
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return []Violation{{Text: fmt.Sprintf("CLAUDE.md is missing: run hand init %s to restore it", shellquote.Quote(dir))}}
	case err != nil:
		return []Violation{{Text: fmt.Sprintf("read %s: %v", claudeRef, err)}}
	case string(data) != claudeContent:
		return []Violation{{Text: fmt.Sprintf("CLAUDE.md is not the @AGENTS.md pointer: run hand init %s to restore it", shellquote.Quote(dir))}}
	}
	return nil
}
