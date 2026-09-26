package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestValidateClaude(t *testing.T) {
	for _, ok := range []Spec{{"claude", "sonnet", "low"}, {"claude", "opus[1m]", "max"}, {"claude", "claude-opus-5-5", "xhigh"}} {
		if err := Validate(ok, t.TempDir()); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []Spec{{"claude", "gpt-5.5", "low"}, {"claude", "sonnet", "extreme"}, {"gemini", "x", "low"}} {
		if err := Validate(bad, t.TempDir()); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("%+v err = %v", bad, err)
		}
	}
}

const cache = `{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"gpt-5.5","supported_reasoning_levels":[{"effort":"high"}]}]}`

func TestValidateCodexAgainstTheModelCache(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Spec{"codex", "gpt-6-luna", "low"}, home); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Spec{{"codex", "gpt-6-sol", "low"}, {"codex", "gpt-5.5", "low"}} {
		if err := Validate(bad, home); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("%+v err = %v", bad, err)
		}
	}
	if err := Validate(Spec{"codex", "gpt-6-luna", "low"}, t.TempDir()); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "run codex once") {
		t.Fatalf("missing cache err = %v", err)
	}
}

func TestCodexHome(t *testing.T) {
	env := map[string]string{"HOME": "/home/me"}
	getenv := func(k string) string { return env[k] }
	if got := CodexHome(getenv); got != "/home/me/.codex" {
		t.Fatalf("default = %s", got)
	}
	env["CODEX_HOME"] = "/c"
	if got := CodexHome(getenv); got != "/c" {
		t.Fatalf("override = %s", got)
	}
}

func TestArgvIsExact(t *testing.T) {
	c, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, "fix it")
	if err != nil || !slices.Equal(c, []string{"/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "fix it"}) {
		t.Fatalf("claude argv = %q, %v", c, err)
	}
	x, err := Argv("/bin/codex", Spec{"codex", "gpt-6-luna", "medium"}, "fix it")
	if err != nil || !slices.Equal(x, []string{"/bin/codex", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=medium", "fix it"}) {
		t.Fatalf("codex argv = %q, %v", x, err)
	}
	for _, bad := range []string{"", "  ", "-p trick", "a\x00b", strings.Repeat("x", MaxPromptBytes+1)} {
		if _, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, bad); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("prompt %.10q err = %v", bad, err)
		}
	}
}

func TestOpencodeTakesNoModelOrEffort(t *testing.T) {
	if err := Validate(Spec{Harness: "opencode"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Spec{{"opencode", "opencode/big-pickle", ""}, {"opencode", "", "high"}} {
		if err := Validate(bad, t.TempDir()); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "its own configuration") {
			t.Fatalf("%+v err = %v", bad, err)
		}
	}
}

func TestOpencodeArgvPrefillsThePrompt(t *testing.T) {
	o, err := Argv("/usr/bin/opencode", Spec{Harness: "opencode"}, "fix it")
	if err != nil || !slices.Equal(o, []string{"/usr/bin/opencode", "--standalone", "--auto", "--prompt", "fix it"}) {
		t.Fatalf("opencode argv = %q, %v", o, err)
	}
	if _, err := Argv("/bin/gemini", Spec{Harness: "gemini"}, "fix it"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("unknown harness argv err = %v", err)
	}
	if !Prefills("opencode") || Prefills("claude") || Prefills("codex") {
		t.Fatal("only opencode pre-fills its prompt")
	}
}

func TestLookPathWantsAnAbsoluteExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LookPath("codex", "relative:"+dir); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("non-executable err = %v", err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath("codex", "relative:"+dir); err != nil || got != bin {
		t.Fatalf("lookpath = %s, %v", got, err)
	}
}
