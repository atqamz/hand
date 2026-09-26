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

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/skills"
)

var skillRoots = []string{".claude", ".agents", ".grok", ".pi"}

func Install(home string) error {
	files := map[string]string{"AGENTS.md": skills.Bootstrap, "CLAUDE.md": "@AGENTS.md\n"}
	for _, root := range skillRoots {
		files[filepath.Join(root, "skills", "secondhand", "SKILL.md")] = skills.Secondhand
	}
	names := slices.Sorted(maps.Keys(files))
	dir, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer dir.Close()
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
