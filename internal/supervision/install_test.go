package supervision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/sessionhook"
)

func TestRenderAssetQuotesPathsForTheHostLanguage(t *testing.T) {
	rendered := string(renderAsset(HostAssets("opencode")[0], `/opt/my hand\bin`, `/fleet home "quoted"`))
	for _, banned := range []string{"__HAND_EXECUTABLE__", "__HAND_HOME__"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("rendered asset still contains %s", banned)
		}
	}
	for _, want := range []string{`"/opt/my hand\\bin"`, `"/fleet home \"quoted\""`, `"supervision", "wait", "--host", "opencode"`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered asset = %q, want it to contain %q", rendered, want)
		}
	}
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

func TestClaudeStopHookMergeIsConflictSafeAndIdempotent(t *testing.T) {
	home := t.TempDir()

	first, err := InstallClaudeStopHook(home, "/opt/my tools/hand")
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "installed" {
		t.Fatalf("install = %#v, want created", first)
	}

	again, err := InstallClaudeStopHook(home, "/opt/my tools/hand")
	if err != nil {
		t.Fatal(err)
	}
	if again.State != "unchanged" {
		t.Fatalf("re-install = %#v, want idempotent", again)
	}

	settings := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "'/opt/my tools/hand' supervision claude-stop") {
		t.Fatalf("settings = %q, want the quoted spaced path hook command", raw)
	}

	// A pre-existing unrelated operator entry survives byte-for-byte.
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	state, err := sessionhook.State(home, "/opt/my tools/hand", "Stop", ClaudeStopHookArgs)
	if err != nil || state != "installed" {
		t.Fatalf("state = %q, %v; want installed", state, err)
	}
	if _, err := InstallClaudeStopHook(home, "/opt/my tools/hand"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(settings)
	if err != nil || string(after) != string(before) {
		t.Fatalf("idempotent install rewrote settings: before %q after %q (%v)", before, after, err)
	}
}

func TestClaudeStopHookRefusesMalformedSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := "{not an object"
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallClaudeStopHook(home, "/opt/bin/hand")
	if err == nil {
		t.Fatal("got nil, want the malformed-settings refusal")
	}
	if result.State != "conflict" {
		t.Fatalf("result = %#v, want conflict state carrying the diagnostic", result)
	}
	raw, readErr := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if readErr != nil || string(raw) != malformed {
		t.Fatalf("malformed settings were overwritten: %q, %v", raw, readErr)
	}
}
