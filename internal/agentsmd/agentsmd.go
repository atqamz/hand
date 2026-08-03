// Package agentsmd generates and refreshes the AGENTS.md workflow/rules
// template that hand init writes into a fleet home, and checks an existing
// one for perishable content and generated-block drift (hand doctor), both
// described in SPECS.md's "AGENTS.md (target)" section.
package agentsmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/home"
)

const (
	filename    = "AGENTS.md"
	symlinkName = "CLAUDE.md"

	beginMarker = "<!-- hand:generated:start -->"
	endMarker   = "<!-- hand:generated:end -->"
)

// OperatorDecisionRule is the one AGENTS.md rule a worker needs wherever it
// runs. A worker's worktree is never under the fleet home, so it never loads
// the home's AGENTS.md; internal/harness puts this same string in the launch
// prompt, and generatedBody embeds it, so the two copies cannot drift.
const OperatorDecisionRule = "Only a `hand send` message carries an operator decision. " +
	"Answering your own harness's question dialog is you deciding, not the operator - " +
	"never record that answer as \"operator said\" or \"operator chose\". " +
	"You may decide anything reversible yourself and proceed; say so in first person - " +
	"`working: deciding myself: <the call> because <reason>` - " +
	"and reserve `needs-decision:` for what you cannot take back."

const generatedBody = `# Secondhand

You manage a fleet of coding agents using the ` + "`hand`" + ` CLI.
Run ` + "`hand --help`" + ` for the full command reference.

## Workflow

1. Run ` + "`hand status`" + ` for current fleet state.
2. Match the request to a project in ` + "`data/projects.md`" + `.
3. Edit ` + "`data/backlog.md`" + ` to record the task with a unique ID.
4. Write a brief at ` + "`data/<id>/brief.md`" + `, including the absolute path to ` + "`state/<id>.status`" + ` and the report vocabulary the worker should append to it. The brief may open with a ` + "`---`" + ` fenced block declaring ` + "`model`" + ` and ` + "`effort`" + ` for the task, which spawn and promote apply unless a flag overrides them.
5. ` + "`hand spawn <id> <project>`" + ` to start a worker.
6. ` + "`hand watch --until-event`" + ` as a background task to monitor the fleet. It exits on the first fleet event and that exit is what reaches you, so re-arm it every time you act on one. Bound the wait with ` + "`--timeout <duration>`" + `; exit 4 means the window passed with nothing happening, exit 5 means a worker named on stderr couldn't be reached before it even started waiting.
7. Act on watch output: steer blocked workers with ` + "`hand send`" + `, relay results.
8. When told to merge: ` + "`hand merge <id>`" + `.
9. ` + "`hand teardown <id>`" + ` after work is landed.

## Rules

- Never edit files under ` + "`projects/`" + `. Workers do that in worktrees.
- Never merge without explicit authorization.
- Never force-teardown without explicit authorization.
- Report outcomes plainly. If work failed, say so with evidence.
- ` + OperatorDecisionRule + `
- Waiting on the operator or on another task: ` + "`hand hold set <id> --kind operator --reason <text>`" + ` or ` + "`--kind blocked --reason <text> --blocked-on <id>`" + `, and ` + "`hand hold clear <id>`" + ` once resolved.
- Name a path in a brief, a status report, or an operator message: full and absolute, never relative. ` + "`hand`" + ` resolves the home from ` + "`HAND_HOME`" + ` or the nearest fleet home at or above the working directory, and a project clone can share its name with the home itself, so a relative path resolves against whichever directory happens to be current.
- Ship tasks produce PRs or local branches. Scout tasks produce ` + "`data/<id>/report.md`" + `.
- ` + "`data/backlog.md`" + ` is your task queue. Edit it directly.
- For no-mistakes projects, workers use ` + "`no-mistakes axi`" + ` directly in the worktree.
- Use ` + "`hand search <query>`" + ` to find historical context in data/. ` + "`qmd search`" + ` adds semantic matching when installed.
- ` + "`hand status <id>`" + ` shows a worker's reported state; see SPECS.md's state management section for the report vocabulary (working/paused/blocked/needs-decision/done/failed).
`

// Refresh writes or refreshes dir/AGENTS.md and its CLAUDE.md symlink,
// reporting whether the template content actually changed (false, nil when dir
// is not a fleet home, which is not an error). An existing AGENTS.md keeps
// everything outside the generated markers untouched, so user-added content and
// extra rules survive; only the span between the markers is replaced, never the
// whole file. A file whose markers are already current, and a marker-less file
// mergeGenerated declines to touch, are both left on disk untouched.
func Refresh(dir string) (bool, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return false, err
	}
	if !isHome {
		return false, nil
	}

	path := filepath.Join(dir, filename)
	existing, err := os.ReadFile(path)
	var target string
	switch {
	case os.IsNotExist(err):
		existing = nil
		target = generatedBlock()
	case err != nil:
		return false, fmt.Errorf("read %s: %w", filename, err)
	default:
		target = mergeGenerated(string(existing))
	}

	refreshed := false
	if target != string(existing) {
		if err := atomicfile.Write(path, ".agents.md-", []byte(target), 0o644); err != nil {
			return false, fmt.Errorf("write %s: %w", filename, err)
		}
		refreshed = true
	}

	symlinkPath := filepath.Join(dir, symlinkName)
	if _, err := os.Lstat(symlinkPath); os.IsNotExist(err) {
		if err := os.Symlink(filename, symlinkPath); err != nil {
			return false, fmt.Errorf("create %s symlink: %w", symlinkName, err)
		}
	} else if err != nil {
		return false, fmt.Errorf("check %s: %w", symlinkName, err)
	}

	return refreshed, nil
}

func generatedBlock() string {
	return beginMarker + "\n" + generatedBody + endMarker + "\n"
}

// mergeGenerated replaces the span between the generated markers with the
// current template, leaving everything before and after untouched. A file
// with no markers (never refreshed by this mechanism) is left as-is rather
// than risk clobbering hand-written content.
func mergeGenerated(content string) string {
	start, end, ok := generatedBlockSpan(content)
	if !ok {
		return content
	}
	return content[:start] + strings.TrimSuffix(generatedBlock(), "\n") + content[end:]
}

// generatedBlockSpan returns the byte range of the generated block, including
// its markers, or ok=false when the markers are absent or malformed (an end
// marker missing, or appearing before any begin marker).
func generatedBlockSpan(content string) (start, end int, ok bool) {
	start = strings.Index(content, beginMarker)
	if start == -1 {
		return 0, 0, false
	}
	relEnd := strings.Index(content[start:], endMarker)
	if relEnd == -1 {
		return 0, 0, false
	}
	return start, start + relEnd + len(endMarker), true
}

var (
	// dateRe and selfExpiringRe describe the two shapes of perishable content
	// that belong in the fleet home's own notes, not AGENTS.md: a dated fact
	// is an incident, and phrasing that names its own expiry is not an
	// invariant. hand does not create or own that notes convention, so
	// neither the violation text nor SPECS.md names a specific path for it.
	dateRe         = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	selfExpiringRe = regexp.MustCompile(`(?i)\b(?:until|once)\s+#\d+\s+lands\b|\bawaiting\s+#\d+\b`)

	// inlineCodeRe and urlRe are stripped from a line before it's tested against
	// dateRe/selfExpiringRe: a date in a quoted example or a URL is not an
	// incident, and flagging it anyway is the false positive that gets a
	// checker ignored (atqamz/secondhand#90).
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
	urlRe        = regexp.MustCompile(`https?://\S+`)
)

// Severity distinguishes a Violation that fails hand doctor from one that is
// informational: reported because it is real and worth a human's attention,
// but not something the checker can resolve into a pass/fail verdict on its
// own - the marker-less case below is the only one so far, since Check has
// no way to tell a file left marker-less by accident from one left that way
// on purpose.
type Severity int

const (
	SeverityViolation Severity = iota
	SeverityInfo
)

// Violation is one perishable-content, malformed-file, or generated-block hit
// Check found, at SeverityViolation unless Severity says otherwise. Line is
// 1-based, or 0 for a violation that isn't about a single line (a drifted or
// absent generated block).
type Violation struct {
	Line     int
	Text     string
	Severity Severity
}

// Check reports perishable content, an unterminated code fence, and either
// generated-block drift or generated markers absent altogether in dir's
// AGENTS.md, described in SPECS.md's "AGENTS.md (target)" section
// (atqamz/secondhand#90). Absent markers come back at SeverityInfo rather
// than SeverityViolation, since Check cannot tell a marker-less file left
// that way by accident from one left that way on purpose. It never writes:
// the point is to make a human look at prose judgment a machine cannot make,
// not to rewrite it. A nil result with no error means dir is not a fleet
// home, or has no AGENTS.md yet - both are an absence, not a violation.
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
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filename, err)
	}
	content := string(data)
	blockStart, blockEnd, hasBlock := generatedBlockSpan(content)

	var violations []Violation
	inFence := false
	fenceOpenedAt := 0
	offset := 0
	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1
		insideBlock := hasBlock && offset >= blockStart && offset < blockEnd
		offset += len(line) + 1

		if r, found := firstBannedRune(line); found {
			violations = append(violations, Violation{Line: lineNo, Text: fmt.Sprintf("banned character %q: no em dash or emoji, house rule everywhere in this file", r)})
		}

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			if inFence {
				fenceOpenedAt = lineNo
			}
			continue
		}
		if inFence || insideBlock {
			continue
		}

		stripped := urlRe.ReplaceAllString(inlineCodeRe.ReplaceAllString(line, ""), "")
		if dateRe.MatchString(stripped) {
			violations = append(violations, Violation{Line: lineNo, Text: "date outside the generated block: a dated fact is an incident, belongs in the home's own notes, not the generated block"})
		}
		if selfExpiringRe.MatchString(stripped) {
			violations = append(violations, Violation{Line: lineNo, Text: "self-expiring phrasing outside the generated block: not an invariant, belongs in the home's own notes, not the generated block"})
		}
	}

	// An unterminated fence silences every date and self-expiring check after
	// it, so it has to be reported rather than left to read as a clean file.
	if inFence {
		violations = append(violations, Violation{Line: fenceOpenedAt, Text: "unterminated code fence: every date and self-expiring check after this line was skipped"})
	}

	switch {
	case !hasBlock:
		violations = append(violations, Violation{
			Text:     "no hand:generated markers: hand init and hand update leave a marker-less file alone, so this template can never refresh itself here - paste the current generated block back in if that is unintended, or ignore this finding if the file is deliberately hand-authored (see SPECS.md's \"AGENTS.md (target)\" section)",
			Severity: SeverityInfo,
		})
	case content[blockStart:blockEnd] != strings.TrimSuffix(generatedBlock(), "\n"):
		violations = append(violations, Violation{Text: fmt.Sprintf("generated block has drifted from generatedBody: run hand init %s to refresh", dir)})
	}

	return violations, nil
}

func firstBannedRune(line string) (rune, bool) {
	for _, r := range line {
		if r == '—' || isEmojiRune(r) {
			return r, true
		}
	}
	return 0, false
}

// isEmojiRune covers the Unicode blocks an accidental emoji actually comes
// from, not a formal emoji property table: pictographs, symbols/dingbats,
// regional-indicator flag letters, and the variation-selector/ZWJ modifiers
// that ride along with them.
func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r >= 0x1F1E6 && r <= 0x1F1FF:
		return true
	case r == 0xFE0F || r == 0x200D:
		return true
	}
	return false
}
