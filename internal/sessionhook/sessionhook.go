// Package sessionhook merges and retires Hand-owned Claude Code hooks in a
// project settings.json, preserving every unrelated operator entry byte for
// byte and refusing any shape it cannot carry through losslessly.
package sessionhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/shellquote"
)

const (
	settingsDir  = ".claude"
	settingsFile = "settings.json"
	legacyEvent  = "SessionStart"
	toolName     = "hand"
	toolNameExe  = "hand.exe"
)

// Removes owned SessionStart hooks from dir/.claude/settings.json and reports
// whether the file changed. A missing settings file is already retired.
func Remove(dir, exe string) (bool, error) {
	return mutate(dir, legacyEvent, func(matchers []any, _ string) ([]any, bool, error) {
		return filterOwned(matchers, exe, legacyEvent)
	})
}

// Ensure merges exactly one Hand-owned command hook under hooks.<event>,
// replacing any earlier Hand-owned entry for that event and preserving every
// unrelated matcher, hook, and setting. Repeated calls are idempotent.
func Ensure(dir, exe, event, args string) (bool, error) {
	command := shellquote.Quote(exe)
	if args != "" {
		command += " " + args
	}
	changed, err := mutate(dir, event, func(matchers []any, _ string) ([]any, bool, error) {
		filtered, _, err := filterOwned(matchers, exe, event)
		if err != nil {
			return nil, false, err
		}
		if exactPresent, ownedCount := scanOwned(matchers, exe, command); exactPresent && ownedCount == 1 {
			return filtered, false, nil
		}
		return append(filtered, map[string]any{
			"matcher": "",
			"hooks":   []any{map[string]any{"type": "command", "command": command}},
		}), true, nil
	})
	return changed, err
}

// Counts Hand-owned entries under the matchers and reports whether one of
// them already runs exactly this command line.
func scanOwned(matchers []any, exe, command string) (exactPresent bool, owned int) {
	for _, matcher := range matchers {
		entry, ok := matcher.(map[string]any)
		if !ok {
			continue
		}
		commands, err := array(entry, "hooks", "hooks")
		if err != nil {
			continue
		}
		for _, hook := range commands {
			hookMap, ok := hook.(map[string]any)
			if !ok {
				continue
			}
			line, ok := hookMap["command"].(string)
			if !ok {
				continue
			}
			args, ownedEntry := handArgs(line, exe)
			if !ownedEntry {
				continue
			}
			owned++
			if strings.TrimSpace(exe)+args == strings.TrimSpace(command) || strings.TrimSpace(line) == strings.TrimSpace(command) {
				exactPresent = true
			}
		}
	}
	return exactPresent, owned
}

// State reports whether hooks.<event> carries a Hand-owned entry running this
// binary with the expected args: installed, stale, or absent. An unparseable
// settings file is malformed - an actionable diagnostic, never overwritten.
func State(dir, exe, event, args string) (string, error) {
	settings, err := read(dir)
	if err != nil {
		return "", err
	}
	if settings == nil {
		return "absent", nil
	}
	hooks, err := object(settings, "hooks", "hooks")
	if err != nil {
		return "", err
	}
	matchers, err := array(hooks, event, "hooks."+event)
	if err != nil {
		return "", err
	}
	command := shellquote.Quote(exe)
	if args != "" {
		command += " " + args
	}
	state := "absent"
	for _, matcher := range matchers {
		entry, ok := matcher.(map[string]any)
		if !ok {
			continue
		}
		commands, err := array(entry, "hooks", "hooks."+event)
		if err != nil {
			return "", err
		}
		for _, hook := range commands {
			command0, ok := hook.(map[string]any)
			if !ok {
				continue
			}
			line, ok := command0["command"].(string)
			if !ok {
				continue
			}
			if _, owned := handArgs(line, exe); !owned {
				continue
			}
			if strings.TrimSpace(line) == strings.TrimSpace(command) {
				return "installed", nil
			}
			state = "stale"
		}
	}
	return state, nil
}

// Parses dir/.claude/settings.json into a settings map. A missing file is nil
// with no error; anything unparseable or non-object is an error the caller
// must surface instead of overwriting.
func read(dir string) (map[string]any, error) {
	existing, err := os.ReadFile(filepath.Join(dir, settingsDir, settingsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relPath(), err)
	}
	var settings map[string]any
	decoder := json.NewDecoder(bytes.NewReader(existing))
	decoder.UseNumber()
	if err := decoder.Decode(&settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath(), err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("parse %s: %w", relPath(), err)
	}
	if settings == nil {
		return nil, fmt.Errorf("%s: settings is not an object, refusing to overwrite it", relPath())
	}
	return settings, nil
}

// Loads settings (starting an empty object when the file does not exist yet),
// applies change to the matchers under hooks.<event>, and writes atomically
// only when something changed.
func mutate(dir, event string, change func(matchers []any, event string) ([]any, bool, error)) (bool, error) {
	settings, err := read(dir)
	if err != nil {
		return false, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, err := object(settings, "hooks", "hooks")
	if err != nil {
		return false, err
	}
	// object hands back an empty stand-in when the key is absent; attach it so
	// a merge into a fresh settings file actually lands.
	if existing, ok := settings["hooks"]; !ok || existing == nil {
		settings["hooks"] = hooks
	}
	matchers, err := array(hooks, event, "hooks."+event)
	if err != nil {
		return false, err
	}
	updated, changed, err := change(matchers, event)
	if err != nil || !changed {
		return false, err
	}
	if len(updated) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = updated
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(dir, settingsDir, settingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", settingsDir, err)
	}
	if err := atomicfile.Write(path, ".settings.json-", encoded, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", relPath(), err)
	}
	return true, nil
}

func relPath() string {
	return filepath.Join(settingsDir, settingsFile)
}

// Strips every Hand-owned command entry from the matchers, carrying every
// unrelated matcher, hook, and setting through untouched. Reports whether
// anything was removed.
func filterOwned(matchers []any, exe, event string) ([]any, bool, error) {
	filteredMatchers := make([]any, 0, len(matchers))
	changed := false
	for i, matcher := range matchers {
		entry, ok := matcher.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: hooks.%s[%d] is not an object, refusing to overwrite it", relPath(), event, i)
		}
		commands, err := array(entry, "hooks", fmt.Sprintf("hooks.%s[%d].hooks", event, i))
		if err != nil {
			return nil, false, err
		}
		filteredCommands := make([]any, 0, len(commands))
		matcherChanged := false
		for j, hook := range commands {
			command, ok := hook.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("%s: hooks.%s[%d].hooks[%d] is not an object, refusing to overwrite it", relPath(), event, i, j)
			}
			rawType, exists := command["type"]
			if !exists {
				filteredCommands = append(filteredCommands, hook)
				continue
			}
			hookType, ok := rawType.(string)
			if !ok {
				return nil, false, fmt.Errorf("%s: hooks.%s[%d].hooks[%d].type is not a string, refusing to overwrite it", relPath(), event, i, j)
			}
			if hookType != "command" {
				filteredCommands = append(filteredCommands, hook)
				continue
			}
			raw, exists := command["command"]
			if !exists {
				filteredCommands = append(filteredCommands, hook)
				continue
			}
			line, ok := raw.(string)
			if !ok {
				return nil, false, fmt.Errorf("%s: hooks.%s[%d].hooks[%d].command is not a string, refusing to overwrite it", relPath(), event, i, j)
			}
			if _, owned := handArgs(line, exe); owned {
				matcherChanged = true
				changed = true
				continue
			}
			filteredCommands = append(filteredCommands, hook)
		}
		if !matcherChanged {
			filteredMatchers = append(filteredMatchers, matcher)
			continue
		}
		if len(filteredCommands) > 0 {
			entry["hooks"] = filteredCommands
			filteredMatchers = append(filteredMatchers, matcher)
		}
	}
	return filteredMatchers, changed, nil
}

// A key whose value is not the shape hand merges into is the operator's, the
// same way an unparseable file is: it cannot be carried through, and writing
// over it destroys what is there. An absent or null key is neither.
func object(m map[string]any, key, path string) (map[string]any, error) {
	raw, ok := m[key]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s is not an object, refusing to overwrite it", relPath(), path)
	}
	return value, nil
}

func array(m map[string]any, key, path string) ([]any, error) {
	raw, ok := m[key]
	if !ok || raw == nil {
		return nil, nil
	}
	value, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s is not an array, refusing to overwrite it", relPath(), path)
	}
	return value, nil
}

// Splits a hook command into whatever follows the binary it runs. An entry is
// ours when it names this binary, or any binary called hand or hand.exe. The
// binary may be shell-quoted, so a path with spaces still resolves.
func handArgs(line, exe string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	first, rest := trimmed, ""
	if len(trimmed) > 0 && (trimmed[0] == '"' || trimmed[0] == '\'') {
		end := strings.IndexByte(trimmed[1:], trimmed[0])
		if end < 0 {
			return "", false
		}
		first = strings.Trim(trimmed[:end+2], string(trimmed[0]))
		rest = strings.TrimLeft(trimmed[end+2:], " \t")
	} else if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		first = trimmed[:i]
		rest = strings.TrimPrefix(trimmed[i:], " ")
	}
	base := filepath.Base(first)
	if first == "" || (first != exe && base != toolName && base != toolNameExe) {
		return "", false
	}
	return rest, true
}
