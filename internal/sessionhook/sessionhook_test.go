package sessionhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mkHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "hand.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, settingsDir, settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings %q: %v", raw, err)
	}
	return settings
}

func hookCommands(t *testing.T, settings map[string]any) []string {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("settings = %+v, want a hooks object", settings)
	}
	matchers, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("hooks = %+v, want a %s array", hooks, event)
	}
	var lines []string
	for _, matcher := range matchers {
		for _, c := range matcher.(map[string]any)["hooks"].([]any) {
			lines = append(lines, c.(map[string]any)["command"].(string))
		}
	}
	return lines
}

func TestRefreshInstallsTheHookInAFreshHome(t *testing.T) {
	dir := mkHome(t)
	changed, err := Refresh(dir, "/opt/bin/hand")
	if err != nil || !changed {
		t.Fatalf("Refresh = %v, %v, want it to report the install", changed, err)
	}
	if got := hookCommands(t, readSettings(t, dir)); len(got) != 1 || got[0] != "/opt/bin/hand" {
		t.Fatalf("commands = %v, want the resolved binary", got)
	}
}

func TestRefreshIsIdempotent(t *testing.T) {
	dir := mkHome(t)
	if _, err := Refresh(dir, "/opt/bin/hand"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, settingsDir, settingsFile))
	if err != nil {
		t.Fatal(err)
	}

	changed, err := Refresh(dir, "/opt/bin/hand")
	if err != nil || changed {
		t.Fatalf("second Refresh = %v, %v, want no change", changed, err)
	}
	after, err := os.ReadFile(filepath.Join(dir, settingsDir, settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("settings changed on a no-op refresh:\n%s\n%s", before, after)
	}
}

// The motivating case for refreshing at all: hand update can leave the binary
// at a different path, and a hook pointing at the old one runs nothing.
func TestRefreshRepointsAMovedBinaryAndKeepsItsArguments(t *testing.T) {
	dir := mkHome(t)
	if _, err := Refresh(dir, "/old/bin/hand"); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, dir)
	matcher := settings["hooks"].(map[string]any)[event].([]any)[0].(map[string]any)
	matcher["hooks"].([]any)[0].(map[string]any)["command"] = "/old/bin/hand status --fields id,reported"
	writeSettings(t, dir, settings)

	changed, err := Refresh(dir, "/new/bin/hand")
	if err != nil || !changed {
		t.Fatalf("Refresh = %v, %v, want it to repoint the hook", changed, err)
	}
	if got := hookCommands(t, readSettings(t, dir)); len(got) != 1 || got[0] != "/new/bin/hand status --fields id,reported" {
		t.Fatalf("commands = %v, want the new path with the operator's arguments intact", got)
	}
}

// A build run under another name is still the same install, so refreshing it
// has to recognize its own path rather than stacking a second entry every run.
func TestRefreshIsIdempotentForABinaryNotNamedHand(t *testing.T) {
	dir := mkHome(t)
	for i := range 2 {
		changed, err := Refresh(dir, "/opt/bin/hand.test")
		if err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		if want := i == 0; changed != want {
			t.Fatalf("run %d changed = %v, want %v", i+1, changed, want)
		}
	}
	if got := hookCommands(t, readSettings(t, dir)); len(got) != 1 {
		t.Fatalf("commands = %v, want exactly one", got)
	}
}

func TestRefreshKeepsEverythingItDoesNotOwn(t *testing.T) {
	dir := mkHome(t)
	writeSettings(t, dir, map[string]any{
		"permissions": map[string]any{"allow": []any{"Bash(git status)"}},
		"hooks": map[string]any{
			event: []any{map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/tea --brew"}},
			}},
			"PreToolUse": []any{map[string]any{"matcher": "Bash"}},
		},
	})

	if _, err := Refresh(dir, "/opt/bin/hand"); err != nil {
		t.Fatal(err)
	}

	settings := readSettings(t, dir)
	if _, ok := settings["permissions"]; !ok {
		t.Fatalf("settings = %+v, want the operator's permissions kept", settings)
	}
	if _, ok := settings["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Fatalf("settings = %+v, want the operator's other hooks kept", settings)
	}
	got := hookCommands(t, settings)
	if len(got) != 2 || got[0] != "/usr/bin/tea --brew" || got[1] != "/opt/bin/hand" {
		t.Fatalf("commands = %v, want hand appended after the operator's own", got)
	}
}

func TestRefreshLeavesADirectoryThatIsNotAFleetHomeAlone(t *testing.T) {
	dir := t.TempDir()
	changed, err := Refresh(dir, "/opt/bin/hand")
	if err != nil || changed {
		t.Fatalf("Refresh = %v, %v, want it to do nothing outside a fleet home", changed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, settingsDir)); !os.IsNotExist(err) {
		t.Fatalf("stat %s = %v, want no settings directory created", settingsDir, err)
	}
}

// A settings file hand cannot parse is the operator's, and rewriting it would
// destroy whatever is in there.
func TestRefreshRefusesToOverwriteUnparseableSettings(t *testing.T) {
	dir := mkHome(t)
	if err := os.MkdirAll(filepath.Join(dir, settingsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, settingsDir, settingsFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(dir, "/opt/bin/hand"); err == nil {
		t.Fatal("Refresh = nil, want an error naming the unparseable file")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Fatalf("settings = %q, %v, want the file untouched", raw, err)
	}
}

func writeSettings(t *testing.T, dir string, settings map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, settingsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsDir, settingsFile), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
