package harness

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestValidateClaude(t *testing.T) {
	for _, ok := range []Spec{{"claude", "sonnet", "low"}, {"claude", "opus[1m]", "max"}, {"claude", "claude-opus-5-5", "xhigh"}} {
		if err := Validate(ok, Env{CodexHome: t.TempDir()}); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []Spec{{"claude", "gpt-5.5", "low"}, {"claude", "sonnet", "extreme"}, {"gemini", "x", "low"}} {
		if err := Validate(bad, Env{CodexHome: t.TempDir()}); !errors.Is(err, state.ErrInvalid) {
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
	if err := Validate(Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: home}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Spec{{"codex", "gpt-6-sol", "low"}, {"codex", "gpt-5.5", "low"}} {
		if err := Validate(bad, Env{CodexHome: home}); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("%+v err = %v", bad, err)
		}
	}
	if err := Validate(Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: t.TempDir()}); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "run codex once") {
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
	if err := Validate(Spec{Harness: "opencode"}, Env{CodexHome: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Spec{{"opencode", "opencode/big-pickle", ""}, {"opencode", "", "high"}} {
		if err := Validate(bad, Env{CodexHome: t.TempDir()}); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "its own configuration") {
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

func TestModelsPerHarness(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	claude, err := Models("claude", Env{CodexHome: home})
	if err != nil || len(claude) != 4 || claude[0].Name != "opus" || !slices.Equal(claude[3].Efforts, claudeEfforts) {
		t.Fatalf("claude = %+v, %v", claude, err)
	}
	codex, err := Models("codex", Env{CodexHome: home})
	if err != nil || len(codex) != 2 || codex[0].Name != "gpt-6-luna" || !slices.Equal(codex[0].Efforts, []string{"low", "medium"}) || !slices.Equal(codex[1].Efforts, []string{"high"}) {
		t.Fatalf("codex = %+v, %v", codex, err)
	}
	if none, err := Models("opencode", Env{CodexHome: home}); err != nil || none != nil {
		t.Fatalf("opencode = %+v, %v", none, err)
	}
	if _, err := Models("codex", Env{CodexHome: t.TempDir()}); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("codex without a cache = %v", err)
	}
}

const agyList = "Fetching available models...\ngemini-3.8-flash-low\tGemini 3.8 Flash (Low)\ngemini-3.1-pro-high\tGemini 3.1 Pro (High)\n"

func fakeAgy(t *testing.T, models string, fail bool) (dir, count string) {
	t.Helper()
	dir = t.TempDir()
	count = filepath.Join(dir, "count")
	code := "0"
	if fail {
		code = "1"
	}
	script := "#!/bin/sh\nif [ \"$1\" = models ]; then echo x >> " + count + "; printf '%s' '" + models + "'; exit " + code + "; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, count
}

func TestValidateAgyAgainstItsModelList(t *testing.T) {
	path, _ := fakeAgy(t, agyList, false)
	env := Env{Path: path}
	if err := Validate(Spec{"agy", "gemini-3.1-pro-high", ""}, env); err != nil {
		t.Fatalf("a listed model = %v", err)
	}
	if err := Validate(Spec{"agy", "gemini-9", ""}, env); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "is not in `agy models`") {
		t.Fatalf("an unlisted model = %v", err)
	}
	if err := Validate(Spec{"agy", "gemini-3.8-flash-low", "high"}, env); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "effort") {
		t.Fatalf("an effort = %v", err)
	}
	bad, _ := fakeAgy(t, "", true)
	if err := Validate(Spec{"agy", "gemini-3.8-flash-low", ""}, Env{Path: bad}); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "check that agy is logged in") {
		t.Fatalf("a failing listing = %v", err)
	}
}

func TestAgyArgvIsExact(t *testing.T) {
	got, err := Argv("/bin/agy", Spec{"agy", "gemini-3.8-flash-low", ""}, "brief")
	if want := []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i", "brief"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
}

func TestAgyModelsAreListed(t *testing.T) {
	path, _ := fakeAgy(t, agyList, false)
	got, err := Models("agy", Env{Path: path})
	if err != nil || len(got) != 2 || got[0].Name != "gemini-3.8-flash-low" || got[1].Name != "gemini-3.1-pro-high" || got[0].Efforts != nil {
		t.Fatalf("agy models = %+v, %v", got, err)
	}
}

func TestAgyModelsFailureIsListedOnceWithItsReason(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\necho x >> " + count + "\necho 'not signed in' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Models("agy", Env{Path: dir}); err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "check that agy is logged in") {
			t.Fatalf("err = %v", err)
		}
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "x") != 1 {
		t.Fatalf("agy models ran %q times, %v", b, err)
	}
}

func TestAgyModelsAreListedOnce(t *testing.T) {
	path, count := fakeAgy(t, agyList, false)
	for range 2 {
		if _, err := Models("agy", Env{Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "x") != 1 {
		t.Fatalf("agy models ran %q times, %v", b, err)
	}
}

func TestAgyModelsFailureIsRetriedAfterAWhile(t *testing.T) {
	path, count := fakeAgy(t, agyList, true)
	if _, err := Models("agy", Env{Path: path}); err == nil {
		t.Fatal("a failing listing succeeded")
	}
	fixed := "#!/bin/sh\nif [ \"$1\" = models ]; then echo x >> " + count + "; printf '%s' '" + agyList + "'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(path, "agy"), []byte(fixed), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Models("agy", Env{Path: path}); err == nil {
		t.Fatal("a cached failure was not kept within agyRetry")
	}
	defer func(d time.Duration) { agyRetry = d }(agyRetry)
	agyRetry = 0
	if m, err := Models("agy", Env{Path: path}); err != nil || len(m) != 2 {
		t.Fatalf("after agyRetry = %v, %v", m, err)
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "x") != 2 {
		t.Fatalf("agy models ran %q times, %v", b, err)
	}
}
