// Package agentsmd generates and refreshes the AGENTS.md workflow/rules
// template that hand init writes into a workspace, described in SPECS.md's
// "AGENTS.md (target)" section.
package agentsmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
4. Write a brief at ` + "`data/<id>/brief.md`" + `.
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
- Ship tasks produce PRs or local branches. Scout tasks produce ` + "`data/<id>/report.md`" + `.
- ` + "`data/backlog.md`" + ` is your task queue. Edit it directly.
- For no-mistakes projects, workers use ` + "`no-mistakes axi`" + ` directly in the worktree.
- Use ` + "`qmd search`" + ` to find historical context in data/ when available. Fall back to reading files directly.
`

// IsWorkspace reports whether dir has been initialized by hand init: the
// presence of data/dashboard.md is the concrete, unambiguous signal since
// hand init is the only thing that writes it.
func IsWorkspace(dir string) (bool, error) {
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
// reporting whether it did (false, nil when dir is not a workspace, which is
// not an error). An existing AGENTS.md keeps everything outside the generated
// markers untouched, so user-added content and extra rules survive; only the
// span between the markers is replaced, never the whole file.
func Refresh(dir string) (bool, error) {
	isWorkspace, err := IsWorkspace(dir)
	if err != nil {
		return false, err
	}
	if !isWorkspace {
		return false, nil
	}

	path := filepath.Join(dir, filename)
	existing, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := writeAtomic(path, []byte(generatedBlock())); err != nil {
			return false, fmt.Errorf("write %s: %w", filename, err)
		}
	case err != nil:
		return false, fmt.Errorf("read %s: %w", filename, err)
	default:
		if err := writeAtomic(path, []byte(mergeGenerated(string(existing)))); err != nil {
			return false, fmt.Errorf("write %s: %w", filename, err)
		}
	}

	symlinkPath := filepath.Join(dir, symlinkName)
	if _, err := os.Lstat(symlinkPath); os.IsNotExist(err) {
		if err := os.Symlink(filename, symlinkPath); err != nil {
			return false, fmt.Errorf("create %s symlink: %w", symlinkName, err)
		}
	} else if err != nil {
		return false, fmt.Errorf("check %s: %w", symlinkName, err)
	}

	return true, nil
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

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agents.md-")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	n, err := tmp.Write(data)
	if err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write temp file: %w", err)
	}
	if n != len(data) {
		_ = tmp.Close()
		removeTemp()
		return io.ErrShortWrite
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		removeTemp()
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
