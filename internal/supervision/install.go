package supervision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
)

// OwnershipMarker is the first line every managed host script asset starts
// with. A destination file without it is foreign content at a Hand-managed
// path, which is a conflict to surface, never permission to overwrite.
const OwnershipMarker = "// Hand-owned supervisor wake integration"

// ClaudeStopArgs is the argv after the executable that the Hand-owned Claude
// Stop hook runs, in upstream exec form: no shell anywhere.
var ClaudeStopArgs = []string{"supervision", "claude-stop"}

// InstallResult is one managed surface's outcome.
type InstallResult struct {
	Host   string
	Path   string
	State  string // installed, unchanged, replaced, conflict, absent, stale
	Detail string
}

func claudeStopHandler(exe string) map[string]any {
	return map[string]any{
		"type":        "command",
		"command":     exe,
		"args":        append([]string(nil), ClaudeStopArgs...),
		"asyncRewake": true,
		"timeout":     1800,
	}
}

// Classifies an existing Stop handler: exact matches the canonical handler,
// owned is a superseded Hand version (including earlier shell-form entries),
// and neither means foreign content.
func claudeOwned(handler map[string]any, exe string) (exact bool, owned bool) {
	if handler["type"] != "command" {
		return false, false
	}
	rawArgs, hasArgs := handler["args"].([]any)
	command, _ := handler["command"].(string)
	if hasArgs {
		args := make([]string, 0, len(rawArgs))
		for _, arg := range rawArgs {
			s, ok := arg.(string)
			if !ok {
				return false, false
			}
			args = append(args, s)
		}
		if !sameStrings(args, ClaudeStopArgs) {
			return false, false
		}
		if command != exe {
			return false, false
		}
		return sameHandler(handler, claudeStopHandler(exe)), true
	}
	// Legacy shell form from earlier integration generations: ownership means
	// this binary followed by our subcommand tokens in the command string.
	first, rest := splitFirstToken(command)
	unquoted := unquoteToken(first)
	base := filepath.Base(unquoted)
	if first == "" || rest == "" || (unquoted != exe && base != "hand" && base != "hand.exe") {
		return false, false
	}
	if strings.HasPrefix(rest, "supervision claude-stop") {
		return false, true
	}
	return false, false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameHandler(a, b map[string]any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(encodedA, encodedB)
}

func splitFirstToken(command string) (first, rest string) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return "", ""
	}
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		end := strings.IndexByte(trimmed[1:], trimmed[0])
		if end < 0 {
			return trimmed, ""
		}
		return trimmed[:end+2], strings.TrimLeft(trimmed[end+2:], " \t")
	}
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		return trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}
	return trimmed, ""
}

// Strips one matching layer of surrounding shell quotes so quoted-path
// ownership checks compare real paths.
func unquoteToken(token string) string {
	if len(token) >= 2 && (token[0] == '"' || token[0] == '\'') && token[len(token)-1] == token[0] {
		return token[1 : len(token)-1]
	}
	return token
}

// InstallClaudeStopHook merges the Hand-owned Stop handler into the fleet
// home's .claude/settings.json: unrelated hooks pass through, owned stale
// versions are replaced, uncarryable shapes refused. Exec form + asyncRewake.
func InstallClaudeStopHook(home, exe string) (InstallResult, error) {
	path := filepath.Join(home, ".claude", "settings.json")
	state, err := mergeClaudeSettings(path, exe)
	return InstallResult{Host: "claude", Path: path, State: state.State, Detail: state.Detail}, err
}

// CheckClaudeStopHook reports the Stop hook state without writing anything.
func CheckClaudeStopHook(home, exe string) (InstallResult, error) {
	path := filepath.Join(home, ".claude", "settings.json")
	state := checkClaudeSettings(filepath.Join(home, ".claude", "settings.json"), exe)
	return InstallResult{Host: "claude", Path: path, State: state.State, Detail: state.Detail}, nil
}

type mergeState struct {
	State  string
	Detail string
}

func readJSONSettings(path string) (map[string]any, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var settings map[string]any
	if json.Unmarshal(existing, &settings) != nil || settings == nil {
		return nil, fmt.Errorf("%s is not a JSON object, refusing to overwrite it", path)
	}
	return settings, nil
}

func writeJSONSettings(path string, settings map[string]any) error {
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := atomicfile.Write(path, ".settings.json-", append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Walks hooks.Stop groups only. Stop has no matcher support upstream, so the
// generated group carries none; foreign groups keep theirs verbatim.
func mergeClaudeSettings(path, exe string) (mergeState, error) {
	settings, err := readJSONSettings(path)
	if err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok && settings["hooks"] != nil {
		err := fmt.Errorf("%s: hooks is not an object, refusing to overwrite it", path)
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	groups, ok := hooks["Stop"].([]any)
	if !ok && hooks["Stop"] != nil {
		err := fmt.Errorf("%s: hooks.Stop is not an array, refusing to overwrite it", path)
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}

	canonical := claudeStopHandler(exe)
	filtered := make([]any, 0, len(groups))
	replaced := false
	for i, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			err := fmt.Errorf("%s: hooks.Stop[%d] is not an object, refusing to overwrite it", path, i)
			return mergeState{State: "conflict", Detail: err.Error()}, err
		}
		handlers, ok := entry["hooks"].([]any)
		if entry["hooks"] != nil && !ok {
			err := fmt.Errorf("%s: hooks.Stop[%d].hooks is not an array, refusing to overwrite it", path, i)
			return mergeState{State: "conflict", Detail: err.Error()}, err
		}
		kept := make([]any, 0, len(handlers))
		groupChanged := false
		for j, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok {
				err := fmt.Errorf("%s: hooks.Stop[%d].hooks[%d] is not an object, refusing to overwrite it", path, i, j)
				return mergeState{State: "conflict", Detail: err.Error()}, err
			}
			exact, owned := claudeOwned(handler, exe)
			if !owned {
				kept = append(kept, handler)
				continue
			}
			groupChanged = true
			replaced = true
			if exact {
				replaced = false
			}
		}
		if groupChanged {
			if len(kept) > 0 {
				entry["hooks"] = kept
				filtered = append(filtered, entry)
			}
			continue
		}
		filtered = append(filtered, entry)
	}

	if !replaced {
		state := checkClaudeSettings(path, exe)
		if state.State == "installed" {
			return mergeState{State: "unchanged", Detail: state.Detail}, nil
		}
		filtered = append(filtered, map[string]any{"hooks": []any{canonical}})
	} else {
		filtered = append(filtered, map[string]any{"hooks": []any{canonical}})
	}
	hooks["Stop"] = filtered
	if err := writeJSONSettings(path, settings); err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if replaced {
		return mergeState{State: "replaced"}, nil
	}
	return mergeState{State: "installed"}, nil
}

// Inspects hooks.Stop for a Hand-owned handler running this binary: installed
// when canonical, stale for a superseded owned version, absent otherwise.
// Malformed config is conflict - actionable, never overwritten blind.
func checkClaudeSettings(path, exe string) mergeState {
	settings, err := readJSONSettings(path)
	if err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}
	}
	if settings == nil {
		return mergeState{State: "absent"}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		if settings["hooks"] != nil {
			return mergeState{State: "conflict", Detail: "hooks is not an object"}
		}
		return mergeState{State: "absent"}
	}
	groups, ok := hooks["Stop"].([]any)
	if !ok {
		if hooks["Stop"] != nil {
			return mergeState{State: "conflict", Detail: "hooks.Stop is not an array"}
		}
		return mergeState{State: "absent"}
	}
	found := "absent"
	for _, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			continue
		}
		handlers, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for _, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			exact, owned := claudeOwned(handler, exe)
			if !owned {
				continue
			}
			if exact {
				return mergeState{State: "installed"}
			}
			found = "stale"
		}
	}
	return mergeState{State: found}
}

// The canonical Codex Stop group entry: upstream embeds arguments in the
// command string, so both platform variants are shell-quoted here; async
// keeps it background work owned by the Codex hook lifecycle.
func codexStopHandler(exe string) map[string]any {
	return map[string]any{
		"type":           "command",
		"command":        shellquoteQuote(exe) + " supervision codex-stop",
		"commandWindows": windowsQuote(exe) + " supervision codex-stop",
		"async":          true,
		"timeout":        1860,
	}
}

func shellquoteQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func windowsQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func codexHooksPath(home string) string { return filepath.Join(home, ".codex", "hooks.json") }

// Classifies one existing Stop handler against this binary's codex-stop
// entrypoint.
func codexOwned(handler map[string]any, exe string) (exact bool, owned bool) {
	if handler["type"] != "command" {
		return false, false
	}
	canonical := codexStopHandler(exe)
	command, _ := handler["command"].(string)
	if !codexCommandTokens(command, exe) {
		return false, false
	}
	if windows, _ := handler["commandWindows"].(string); windows != "" && !codexCommandTokens(windows, exe) {
		return false, false
	}
	if _, isAsync := handler["async"].(bool); !isAsync {
		return false, true
	}
	return sameHandler(handler, canonical), true
}

func codexCommandTokens(command, exe string) bool {
	first, rest := splitFirstToken(command)
	if first == "" || rest != "supervision codex-stop" {
		return false
	}
	unquoted := unquoteToken(first)
	base := filepath.Base(unquoted)
	return unquoted == exe || base == "hand" || base == "hand.exe"
}

// InstallCodexHooks merges the Hand-owned async Stop group into the
// Fleet-local .codex/hooks.json: foreign events and handlers pass through,
// an owned superseded version is replaced, anything unparseable is refused.
func InstallCodexHooks(home, exe string) (InstallResult, error) {
	path := codexHooksPath(home)
	state, err := mergeCodexHooks(path, exe)
	return InstallResult{Host: "codex", Path: path, State: state.State, Detail: state.Detail}, err
}

// CheckCodexHooks reports the Fleet-local codex hook state without writing.
func CheckCodexHooks(home, exe string) (InstallResult, error) {
	path := codexHooksPath(home)
	state := inspectCodexHooks(path, exe)
	return InstallResult{Host: "codex", Path: path, State: state.State, Detail: state.Detail}, nil
}

func mergeCodexHooks(path, exe string) (mergeState, error) {
	settings, err := readJSONSettings(path)
	if err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if settings == nil {
		if err := writeJSONSettings(path, renderCodexJSON(exe)); err != nil {
			return mergeState{State: "conflict", Detail: err.Error()}, err
		}
		return mergeState{State: "installed"}, nil
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok && settings["hooks"] != nil {
		err := fmt.Errorf("%s: hooks is not an object, refusing to overwrite it", path)
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	groups, ok := hooks["Stop"].([]any)
	if !ok && hooks["Stop"] != nil {
		err := fmt.Errorf("%s: hooks.Stop is not an array, refusing to overwrite it", path)
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}

	replaced := false
	filtered := make([]any, 0, len(groups)+1)
	for i, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			err := fmt.Errorf("%s: hooks.Stop[%d] is not an object, refusing to overwrite it", path, i)
			return mergeState{State: "conflict", Detail: err.Error()}, err
		}
		handlers, ok := entry["hooks"].([]any)
		if entry["hooks"] != nil && !ok {
			err := fmt.Errorf("%s: hooks.Stop[%d].hooks is not an array, refusing to overwrite it", path, i)
			return mergeState{State: "conflict", Detail: err.Error()}, err
		}
		kept := make([]any, 0, len(handlers))
		groupChanged := false
		for j, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok {
				err := fmt.Errorf("%s: hooks.Stop[%d].hooks[%d] is not an object, refusing to overwrite it", path, i, j)
				return mergeState{State: "conflict", Detail: err.Error()}, err
			}
			exact, owned := codexOwned(handler, exe)
			if !owned {
				kept = append(kept, handler)
				continue
			}
			groupChanged = true
			replaced = true
			if exact {
				replaced = false
			}
		}
		switch {
		case !groupChanged:
			filtered = append(filtered, entry)
		case len(kept) > 0:
			entry["hooks"] = kept
			filtered = append(filtered, entry)
		}
	}

	if !replaced {
		if inspectCodexHandlers(groups, exe) == "installed" {
			return mergeState{State: "unchanged"}, nil
		}
	}
	filtered = append(filtered, map[string]any{"hooks": []any{codexStopHandler(exe)}})
	hooks["Stop"] = filtered
	if err := writeJSONSettings(path, settings); err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}, err
	}
	if replaced {
		return mergeState{State: "replaced"}, nil
	}
	return mergeState{State: "installed"}, nil
}

func renderCodexJSON(exe string) map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{codexStopHandler(exe)}},
			},
		},
	}
}

func inspectCodexHooks(path, exe string) mergeState {
	settings, err := readJSONSettings(path)
	if err != nil {
		return mergeState{State: "conflict", Detail: err.Error()}
	}
	if settings == nil {
		return mergeState{State: "absent"}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		if settings["hooks"] != nil {
			return mergeState{State: "conflict", Detail: "hooks is not an object"}
		}
		return mergeState{State: "absent"}
	}
	groups, ok := hooks["Stop"].([]any)
	if !ok {
		if hooks["Stop"] != nil {
			return mergeState{State: "conflict", Detail: "hooks.Stop is not an array"}
		}
		return mergeState{State: "absent"}
	}
	state := "absent"
	for _, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			continue
		}
		handlers, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for _, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			exact, owned := codexOwned(handler, exe)
			if !owned {
				continue
			}
			if exact {
				return mergeState{State: "installed"}
			}
			state = "stale"
		}
	}
	return mergeState{State: state}
}

func inspectCodexHandlers(groups []any, exe string) string {
	state := "absent"
	for _, group := range groups {
		entry, ok := group.(map[string]any)
		if !ok {
			continue
		}
		handlers, _ := entry["hooks"].([]any)
		for _, raw := range handlers {
			handler, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			exact, owned := codexOwned(handler, exe)
			if exact {
				return "installed"
			}
			if owned {
				state = "stale"
			}
		}
	}
	return state
}

// CheckHostAssets reports managed asset states for one host without writing.
// exe and home are the install-time substitutions canonical bytes are judged
// against.
func CheckHostAssets(home, host, exe string) ([]InstallResult, error) {
	var results []InstallResult
	for _, a := range HostAssets(host) {
		path := filepath.Join(home, a.RelPath)
		existing, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			results = append(results, InstallResult{Host: host, Path: path, State: "absent"})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", a.RelPath, err)
		}
		results = append(results, InstallResult{Host: host, Path: path, State: assetState(existing, renderAsset(a, exe, home))})
	}
	return results, nil
}

// Compares existing bytes to the canonical rendered shape; the ownership
// marker proves ownership: a marked mismatch is a stale Hand version, an
// unmarked file is foreign content.
func assetState(existing, canonical []byte) string {
	if !bytes.HasPrefix(bytes.TrimSpace(existing), []byte(OwnershipMarker)) {
		return "conflict"
	}
	if bytes.Equal(existing, canonical) {
		return "unchanged"
	}
	return "stale"
}

// InstallHostAssets writes the managed assets for one host, replacing only
// Hand-owned stale versions and refusing foreign content at exact managed
// paths. Canonical files stay untouched, so repeated init changes no bytes.
func InstallHostAssets(home, host, exe string) ([]InstallResult, error) {
	var results []InstallResult
	for _, a := range HostAssets(host) {
		path := filepath.Join(home, a.RelPath)
		result := InstallResult{Host: host, Path: path}
		existing, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			result.State = "installed"
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", a.RelPath, err)
		default:
			switch assetState(existing, renderAsset(a, exe, home)) {
			case "conflict":
				result.State = "conflict"
				result.Detail = "a foreign file occupies the Hand-managed path; move it aside, then run hand init again"
				results = append(results, result)
				continue
			case "unchanged":
				result.State = "unchanged"
				results = append(results, result)
				continue
			default:
				result.State = "replaced"
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(a.RelPath), err)
		}
		if err := atomicfile.Write(path, ".hand-supervisor-wake-", renderAsset(a, exe, home), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", a.RelPath, err)
		}
		results = append(results, result)
	}
	return results, nil
}
