package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/skills"
)

var skillRoots = []string{".claude", ".agents", ".grok", ".pi"}

func RefuseBeside07(home string) error {
	dir, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat(filepath.Join("state", "hand.db"))
	switch {
	case err == nil && info.Mode().IsRegular():
		return fmt.Errorf("%w: %s holds Hand 0.7 data, which this hand does not read; run `hand init` in a folder of its own", state.ErrConflict, home)
	case err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("check for Hand 0.7 data: %w", err)
	}
	return nil
}

func Install(home, command string) error {
	dir, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer dir.Close()
	named := strings.NewReplacer("`hand ", "`"+command+" ", "`hand`", "`"+command+"`")
	files := map[string]string{"AGENTS.md": named.Replace(skills.Bootstrap), "CLAUDE.md": "@AGENTS.md\n"}
	for _, root := range skillRoots {
		files[filepath.Join(root, "skills", "secondhand", "SKILL.md")] = named.Replace(skills.Secondhand)
		files[filepath.Join(root, "skills", "secondhand-cleanup", "SKILL.md")] = named.Replace(skills.Cleanup)
	}
	names := slices.Sorted(maps.Keys(files))
	for _, rel := range names {
		have, err := dir.ReadFile(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !owned(rel, string(have)) {
			return fmt.Errorf("%w: %s is not Hand's; move it away or run `hand init` in another folder", state.ErrConflict, filepath.Join(home, rel))
		}
	}
	for _, rel := range names {
		if err := dir.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return err
		}
		if err := dir.WriteFile(rel, []byte(files[rel]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func owned(rel, have string) bool {
	switch rel {
	case "AGENTS.md":
		marker, _, _ := strings.Cut(skills.Bootstrap, "\n")
		return strings.HasPrefix(have, marker+"\n") || strings.HasPrefix(have, "## Secondhand supervisor bootstrap\n")
	case "CLAUDE.md":
		return strings.TrimSpace(have) == "@AGENTS.md"
	}
	return strings.Contains(have, "\n  managed-by: hand\n")
}
