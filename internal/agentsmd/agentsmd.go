// Package agentsmd generates and refreshes the AGENTS.md workflow/rules
// template that hand init writes into a workspace, described in SPECS.md's
// "AGENTS.md (target)" section.
package agentsmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/atomicfile"
)

const (
	filename    = "AGENTS.md"
	symlinkName = "CLAUDE.md"

	beginMarker = "<!-- hand:generated:start -->"
	endMarker   = "<!-- hand:generated:end -->"
)

const generatedBody = `# Secondhand

You manage a fleet of coding agents using the ` + "`hand`" + ` CLI.
Run ` + "`hand --help`" + ` for the full command reference.

## Workflow

1. Read ` + "`data/dashboard.md`" + ` for current fleet state.
2. Match the request to a project in ` + "`data/projects.md`" + `.
3. Edit ` + "`data/backlog.md`" + ` to record the task with a unique ID.
4. Write a brief at ` + "`data/<id>/brief.md`" + `, including the absolute path to ` + "`state/<id>.status`" + ` and the report vocabulary the worker should append to it. The brief may open with a ` + "`---`" + ` fenced block declaring ` + "`model`" + ` and ` + "`effort`" + ` for the task, which spawn and promote apply unless a flag overrides them.
5. ` + "`hand spawn <id> <project>`" + ` to start a worker.
6. ` + "`hand watch`" + ` as a background task to monitor the fleet.
7. Act on watch output: steer blocked workers with ` + "`hand send`" + `, relay results.
8. When told to merge: ` + "`hand merge <id>`" + `.
9. ` + "`hand teardown <id>`" + ` after work is landed.

## Rules

- Never edit files under ` + "`projects/`" + `. Workers do that in worktrees.
- Never merge without explicit authorization.
- Never force-teardown without explicit authorization.
- Report outcomes plainly. If work failed, say so with evidence.
- Name a path in a brief, a status report, or an operator message: full and absolute, never relative. ` + "`hand`" + ` resolves the home from the current working directory, and a project clone can share its name with the home itself, so a relative path resolves against whichever directory happens to be current.
- Ship tasks produce PRs or local branches. Scout tasks produce ` + "`data/<id>/report.md`" + `.
- ` + "`data/backlog.md`" + ` is your task queue. Edit it directly.
- For no-mistakes projects, workers use ` + "`no-mistakes axi`" + ` directly in the worktree.
- Use ` + "`qmd search`" + ` to find historical context in data/ when available. Fall back to reading files directly.
- ` + "`hand status <id>`" + ` shows a worker's reported state; see SPECS.md's state management section for the report vocabulary (working/paused/blocked/needs-decision/done/failed).
`

// isWorkspace reports whether dir has been initialized by hand init: the
// presence of data/dashboard.md is the concrete, unambiguous signal since
// hand init is the only thing that writes it.
func isWorkspace(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, "data", "dashboard.md"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("check workspace: %w", err)
}

// Refresh writes or refreshes dir/AGENTS.md and its CLAUDE.md symlink,
// reporting whether the template content actually changed (false, nil when dir
// is not a workspace, which is not an error). An existing AGENTS.md keeps
// everything outside the generated markers untouched, so user-added content and
// extra rules survive; only the span between the markers is replaced, never the
// whole file. A file whose markers are already current, and a marker-less file
// mergeGenerated declines to touch, are both left on disk untouched.
func Refresh(dir string) (bool, error) {
	workspace, err := isWorkspace(dir)
	if err != nil {
		return false, err
	}
	if !workspace {
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
	start := strings.Index(content, beginMarker)
	if start == -1 {
		return content
	}
	end := strings.Index(content, endMarker)
	if end == -1 || end < start {
		return content
	}
	end += len(endMarker)
	return content[:start] + strings.TrimSuffix(generatedBlock(), "\n") + content[end:]
}
