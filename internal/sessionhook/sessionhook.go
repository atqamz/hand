// Package sessionhook installs hand as a Claude Code SessionStart hook in a
// fleet home, so a supervising agent's session opens with the fleet already in
// context instead of spending a turn asking for it.
package sessionhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/home"
)

const (
	settingsDir  = ".claude"
	settingsFile = "settings.json"
	event        = "SessionStart"
	toolName     = "hand"
)

// Installs or repoints the hook in dir/.claude/settings.json and reports
// whether the file changed. A dir that is not a fleet home is not an error:
// nothing there runs a supervising session.
func Refresh(dir, exe string) (bool, error) {
	isHome, err := home.IsHome(dir)
	if err != nil {
		return false, err
	}
	if !isHome {
		return false, nil
	}

	path := filepath.Join(dir, settingsDir, settingsFile)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", filepath.Join(settingsDir, settingsFile), err)
	}

	settings := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return false, fmt.Errorf("parse %s: %w", filepath.Join(settingsDir, settingsFile), err)
		}
	}
	if !install(settings, exe) {
		return false, nil
	}

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Join(dir, settingsDir), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", settingsDir, err)
	}
	if err := atomicfile.Write(path, ".settings.json-", encoded, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", filepath.Join(settingsDir, settingsFile), err)
	}
	return true, nil
}

// Edits settings in place and reports whether anything changed. Everything it
// does not own is carried through untouched: an operator's own hooks,
// permissions and settings outlive every refresh.
func install(settings map[string]any, exe string) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	matchers, _ := hooks[event].([]any)

	for _, matcher := range matchers {
		entry, _ := matcher.(map[string]any)
		commands, _ := entry["hooks"].([]any)
		for _, c := range commands {
			command, _ := c.(map[string]any)
			line, _ := command["command"].(string)
			args, ok := handArgs(line, exe)
			if !ok {
				continue
			}
			// An operator who added arguments keeps them; only the path to the
			// binary is ours to correct, which is what a moved install needs.
			repointed := exe + args
			if repointed == line {
				return false
			}
			command["command"] = repointed
			return true
		}
	}

	hooks[event] = append(matchers, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": exe}},
	})
	settings["hooks"] = hooks
	return true
}

// Splits a hook command into whatever follows the binary it runs. An entry is
// ours when it names this binary or any binary called hand, so a moved install
// is repointed rather than duplicated.
func handArgs(line, exe string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	first := trimmed
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		first = trimmed[:i]
	}
	if first == "" || (first != exe && filepath.Base(first) != toolName) {
		return "", false
	}
	return strings.TrimPrefix(trimmed, first), true
}
