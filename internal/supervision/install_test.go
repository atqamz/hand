package supervision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRenderAssetSubstitutesExactJSONStringLiterals(t *testing.T) {
	exe := `C:\Program Files\my "hand"\hånd.exe`
	home := `/fleet home's "quoted" path`
	rendered := string(renderAsset(HostAssets("opencode")[0], exe, home))
	for _, banned := range []string{"__HAND_EXECUTABLE__", "__HAND_HOME__", `const HAND_EXE = ""`, `const HAND_HOME = ""`} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("rendered asset contains %s:\n%s", banned, rendered)
		}
	}
	var gotExe string
	if err := json.Unmarshal([]byte(strBetween(rendered, "const HAND_EXE = ", ";")), &gotExe); err != nil {
		t.Fatalf("HAND_EXE is not a valid JSON string literal: %v\n%s", err, rendered)
	}
	if gotExe != exe {
		t.Fatalf("HAND_EXE = %q, want the exact original path %q", gotExe, exe)
	}
	var gotHome string
	if err := json.Unmarshal([]byte(strBetween(rendered, "const HAND_HOME = ", ";")), &gotHome); err != nil {
		t.Fatalf("HAND_HOME is not a valid JSON string literal: %v", err)
	}
	if gotHome != home {
		t.Fatalf("HAND_HOME = %q, want the exact original %q", gotHome, home)
	}
}

func strBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i+len(start):], end)
	if j < 0 {
		return ""
	}
	trimmed := s[i+len(start) : i+len(start)+j]
	return strings.TrimSuffix(trimmed, "\r")
}

func TestInstallHostAssetsIdempotentStaleAndConflict(t *testing.T) {
	home := t.TempDir()
	exe := "/opt/bin/hand"

	first, err := InstallHostAssets(home, "pi", exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].State != "installed" {
		t.Fatalf("first install = %#v, want installed", first)
	}

	second, err := InstallHostAssets(home, "pi", exe)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].State != "unchanged" {
		t.Fatalf("second install = %#v, want a byte-level no-op", second)
	}

	path := filepath.Join(home, HostAssets("pi")[0].RelPath)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := append([]byte("// Hand-owned supervisor wake integration (old version)\n"), body[len(body)-10:]...)
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	replaced, err := InstallHostAssets(home, "pi", exe)
	if err != nil {
		t.Fatal(err)
	}
	if replaced[0].State != "replaced" {
		t.Fatalf("stale install = %#v, want the Hand-owned stale version replaced", replaced)
	}

	foreign := "// an operator's own extension content\n"
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	conflicted, err := InstallHostAssets(home, "pi", exe)
	if err != nil {
		t.Fatal(err)
	}
	if conflicted[0].State != "conflict" || conflicted[0].Detail == "" {
		t.Fatalf("foreign install = %#v, want a surfaced conflict", conflicted)
	}
	kept, err := os.ReadFile(path)
	if err != nil || string(kept) != foreign {
		t.Fatalf("foreign file changed: %q, %v; conflicts are never overwrites", kept, err)
	}
}

func TestClaudeStopHookUsesExecFormAsyncRewake(t *testing.T) {
	home := t.TempDir()
	exe := "/opt/my tools/hand"

	first, err := InstallClaudeStopHook(home, exe)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "installed" {
		t.Fatalf("install = %#v, want created", first)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks struct {
			Stop []struct {
				Hooks []map[string]any `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	handlers := settings.Hooks.Stop
	if len(handlers) != 1 || len(handlers[0].Hooks) != 1 {
		t.Fatalf("hooks.Stop = %+v, want exactly one group with one handler", handlers)
	}
	handler := handlers[0].Hooks[0]
	if handler["type"] != "command" || handler["command"] != exe {
		t.Fatalf("handler = %+v, want exec form naming this binary only", handler)
	}
	args, _ := handler["args"].([]any)
	if !reflect.DeepEqual(args, []any{"supervision", "claude-stop"}) {
		t.Fatalf("args = %+v, want structured argv with no shell anywhere", args)
	}
	if handler["asyncRewake"] != true {
		t.Fatalf("asyncRewake = %+v, want the upstream background rewake primitive enabled", handler["asyncRewake"])
	}

	again, err := InstallClaudeStopHook(home, exe)
	if err != nil || again.State != "unchanged" {
		t.Fatalf("re-install = %#v, %v; want idempotent", again, err)
	}
	before, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if _, err := InstallClaudeStopHook(home, exe); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(before) != string(after) {
		t.Fatal("idempotent install rewrote settings bytes")
	}
}

func TestClaudeStopHookPreservesForeignAndUpgradesOwnedLegacy(t *testing.T) {
	home := t.TempDir()
	foreign := map[string]any{
		"type":    "command",
		"command": "/usr/bin/operator-own-tool --flag",
	}
	legacy := map[string]any{
		"type":    "command",
		"command": "'/old/path/hand' supervision claude-stop",
	}
	writeSettingsJSON(t, filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"permissions": map[string]any{"allow": []string{"Bash(ls*)"}},
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "Bash", "hooks": []any{foreign}}},
			"Stop":       []any{map[string]any{"hooks": []any{legacy}}},
		},
	})

	result, err := InstallClaudeStopHook(home, "/new/install/hand")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "replaced" {
		t.Fatalf("install = %#v, want the owned legacy entry replaced", result)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "/usr/bin/operator-own-tool --flag") || !strings.Contains(text, `"PreToolUse"`) {
		t.Fatalf("settings lost foreign operator content:\n%s", text)
	}
	if strings.Contains(text, "/old/path/hand") {
		t.Fatalf("stale Hand-owned entry survived:\n%s", text)
	}
	if !strings.Contains(text, `"asyncRewake": true`) {
		t.Fatalf("settings missing asyncRewake on the new handler:\n%s", text)
	}
}

func TestClaudeStopHookRefusesMalformedSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := "{not an object"
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallClaudeStopHook(home, "/opt/bin/hand")
	if err == nil {
		t.Fatal("got nil, want the malformed-settings refusal")
	}
	if result.State != "conflict" {
		t.Fatalf("result = %#v, want conflict state carrying the diagnostic", result)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil || string(raw) != malformed {
		t.Fatalf("malformed settings were overwritten: %q, %v", raw, readErr)
	}
}

func TestCodexHooksMergeIsFleetLocalIdempotentAndForeignSafe(t *testing.T) {
	home := t.TempDir()
	exe := "/opt/bin hand/hand"

	first, err := InstallCodexHooks(home, exe)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "installed" {
		t.Fatalf("first install = %#v, want installed", first)
	}
	second, err := InstallCodexHooks(home, exe)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != "unchanged" {
		t.Fatalf("second install = %#v, want idempotent", second)
	}
	raw, err := os.ReadFile(filepath.Join(home, CodexHooksRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks struct {
			Stop []struct {
				Hooks []map[string]any `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("hooks.json invalid: %v", err)
	}
	if len(doc.Hooks.Stop) != 1 || doc.Hooks.Stop[0].Hooks[0]["async"] != true {
		t.Fatalf("hooks.Stop = %+v, want one async background Stop group", doc.Hooks.Stop)
	}

	// A foreign-only document gains our group without losing anything.
	path := filepath.Join(home, CodexHooksRelPath)
	theirs := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "operator-own"}}}},
		},
	}
	theirBytes, _ := json.MarshalIndent(theirs, "", "  ")
	if err := os.WriteFile(path, theirBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := InstallCodexHooks(home, exe)
	if err != nil {
		t.Fatal(err)
	}
	if merged.State != "installed" {
		t.Fatalf("merge into foreign doc = %#v, want our group appended", merged)
	}
	text, _ := os.ReadFile(path)
	if !strings.Contains(string(text), "operator-own") {
		t.Fatalf("foreign codex hook content lost:\n%s", text)
	}

	malformed := "{broken"
	if err := os.WriteFile(path, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}
	refused, err := InstallCodexHooks(home, exe)
	if err == nil || refused.State != "conflict" {
		t.Fatalf("malformed merge = %#v, %v; want refusal", refused, err)
	}
	kept, _ := os.ReadFile(path)
	if string(kept) != malformed {
		t.Fatalf("malformed hooks.json overwritten: %q", kept)
	}
}

func writeSettingsJSON(t *testing.T, path string, body map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
