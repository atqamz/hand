package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"agy": fakeAgyMain, "opencode": fakeOpencodeMain, "echoargs": echoArgsMain})
	os.Exit(m.Run())
}

func fakeAgyMain(args []string) int {
	p := fakebin.Params()
	if held(args) || len(args) == 0 || args[0] != "models" {
		return 0
	}
	if p["count"] != "" {
		fakebin.Append(p["count"], "x")
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	if p["hold"] != "" {
		hold()
	}
	fmt.Fprint(os.Stderr, p["stderr"])
	fmt.Print(p["models"])
	code, _ := strconv.Atoi(p["exit"])
	return code
}

func fakeOpencodeMain(args []string) int {
	p := fakebin.Params()
	switch a := strings.Join(args, " "); {
	case held(args):
	case a == "session list --standalone --format json":
		hold()
		b, err := os.ReadFile(p["list"])
		if err != nil {
			return 1
		}
		wd, _ := os.Getwd()
		q, _ := json.Marshal(wd)
		fmt.Print(strings.ReplaceAll(string(b), "/fleets/demo", string(q[1:len(q)-1])))
	case strings.HasPrefix(a, "session export --standalone "):
		out, ok := p[strings.TrimPrefix(a, "session export --standalone ")]
		if !ok {
			out = p["other"]
		}
		fmt.Println(out)
	default:
		fmt.Fprintln(os.Stderr, "bad args:", a)
		return 1
	}
	return 0
}

func hold() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "hold")
	cmd.Stdout = os.Stdout
	if cmd.Start() == nil {
		fakebin.Append(fakebin.Params()["holders"], strconv.Itoa(cmd.Process.Pid))
	}
}

func held(args []string) bool {
	if len(args) != 1 || args[0] != "hold" {
		return false
	}
	time.Sleep(5 * time.Second)
	return true
}

func holders(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "holders")
	t.Cleanup(func() {
		b, _ := os.ReadFile(file)
		for _, f := range strings.Fields(string(b)) {
			pid, _ := strconv.Atoi(f)
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
				_, _ = p.Wait()
			}
		}
	})
	return file
}

const cache = `{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"gpt-5.5","supported_reasoning_levels":[{"effort":"high"}]}]}`

func TestCheckDecidesBetweenAcceptWarnAndRefuse(t *testing.T) {
	cached := t.TempDir()
	if err := os.WriteFile(filepath.Join(cached, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "models_cache.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed, _ := fakeAgy(t, agyList, false)
	failing, _ := fakeAgy(t, "", true)
	const (
		ok     = ""
		warn   = "warn"
		refuse = "refuse"
	)
	long := strings.Repeat("a", 201)
	rows := []struct {
		name string
		spec Spec
		env  Env
		kind string
		text string
	}{
		{"alias", Spec{"claude", "opus", "low"}, Env{}, ok, ""},
		{"alias with a window", Spec{"claude", "sonnet[1m]", "max"}, Env{}, ok, ""},
		{"claude id", Spec{"claude", "claude-opus-5-5", "xhigh"}, Env{}, ok, ""},
		{"claude id with a window", Spec{"claude", "claude-opus-4-6[1m]", "high"}, Env{}, ok, ""},
		{"gateway name", Spec{"claude", "acme-opus", "high"}, Env{}, warn, "claude model acme-opus is not an alias"},
		{"bedrock id", Spec{"claude", "us.anthropic.claude-opus-4-8", "high"}, Env{}, warn, "us.anthropic.claude-opus-4-8"},
		{"bedrock versioned id", Spec{"claude", "us.anthropic.claude-opus-5-5-v1:0", "high"}, Env{}, warn, "starting anyway"},
		{"arn", Spec{"claude", "arn:aws:bedrock:us-east-1:0:inference-profile/x", "high"}, Env{}, warn, "arn:aws:bedrock"},
		{"vertex id", Spec{"claude", "claude-sonnet-4-5@20250929", "high"}, Env{}, warn, "starting anyway"},
		{"provider prefix", Spec{"claude", "anthropic/claude-opus-4-8", "high"}, Env{}, warn, "starting anyway"},
		{"gateway prefix", Spec{"claude", "my-gateway/claude-opus-5-5", "high"}, Env{}, warn, "starting anyway"},
		{"opusplan", Spec{"claude", "opusplan", "high"}, Env{}, warn, "claude model opusplan"},
		{"best", Spec{"claude", "best", "high"}, Env{}, warn, "claude model best"},
		{"capitals", Spec{"claude", "Claude-Opus-5", "high"}, Env{}, warn, "starting anyway"},
		{"underscore", Spec{"claude", "claude-opus-4-8_v2", "high"}, Env{}, warn, "starting anyway"},
		{"other vendor", Spec{"claude", "glm-5", "high"}, Env{}, warn, "starting anyway"},
		{"unknown effort", Spec{"claude", "sonnet", "ultra"}, Env{}, warn, "effort ultra"},
		{"empty model", Spec{"claude", "", "low"}, Env{}, refuse, "claude model"},
		{"empty effort", Spec{"claude", "sonnet", ""}, Env{}, refuse, "claude effort"},
		{"space", Spec{"claude", "my model", "low"}, Env{}, refuse, "claude model"},
		{"flag", Spec{"claude", "-x", "low"}, Env{}, refuse, "claude model"},
		{"nul", Spec{"claude", "a\x00b", "low"}, Env{}, refuse, "claude model"},
		{"newline", Spec{"claude", "a\nb", "low"}, Env{}, refuse, "claude model"},
		{"tab", Spec{"claude", "a\tb", "low"}, Env{}, refuse, "claude model"},
		{"escape", Spec{"claude", "a\x1b[31m", "low"}, Env{}, refuse, "claude model"},
		{"quote", Spec{"claude", `a"b`, "low"}, Env{}, refuse, "claude model"},
		{"semicolon", Spec{"claude", "a;b", "low"}, Env{}, refuse, "claude model"},
		{"dollar", Spec{"claude", "$HOME", "low"}, Env{}, refuse, "claude model"},
		{"backtick", Spec{"claude", "a`b", "low"}, Env{}, refuse, "claude model"},
		{"too long", Spec{"claude", long, "low"}, Env{}, refuse, "claude model"},
		{"effort with a space", Spec{"claude", "sonnet", "very high"}, Env{}, refuse, "claude effort"},
		{"effort with a flag", Spec{"claude", "sonnet", "--max"}, Env{}, refuse, "claude effort"},
		{"effort with a newline", Spec{"claude", "sonnet", "low\n"}, Env{}, refuse, "claude effort"},
		{"unknown harness", Spec{"gemini", "x", "low"}, Env{}, refuse, `harness "gemini"`},
		{"no harness", Spec{}, Env{}, refuse, "harness"},

		{"codex listed", Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: cached}, ok, ""},
		{"codex unlisted", Spec{"codex", "acme-coder-1", "low"}, Env{CodexHome: cached}, warn, "codex model acme-coder-1 is not in the model cache"},
		{"codex effort not listed", Spec{"codex", "gpt-5.5", "low"}, Env{CodexHome: cached}, warn, "codex model gpt-5.5 lists efforts high, not low"},
		{"codex without a cache", Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: t.TempDir()}, warn, "run codex once"},
		{"codex with a broken cache", Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: broken}, warn, "no usable models_cache.json"},
		{"codex empty model", Spec{"codex", "", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex empty effort", Spec{"codex", "gpt-6-luna", ""}, Env{CodexHome: cached}, refuse, "codex effort"},
		{"codex model with a space", Spec{"codex", "gpt 6", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex flag model", Spec{"codex", "--oss", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex effort with an equals sign", Spec{"codex", "gpt-6-luna", "low=1"}, Env{CodexHome: cached}, refuse, "codex effort"},
		{"codex custom effort", Spec{"codex", "gpt-6-luna", "ultra"}, Env{CodexHome: cached}, warn, "lists efforts low, medium, not ultra"},

		{"agy listed", Spec{"agy", "gemini-3.1-pro-high", ""}, Env{Path: listed}, ok, ""},
		{"agy unlisted", Spec{"agy", "gemini-9", ""}, Env{Path: listed}, warn, "agy model gemini-9 is not in `agy models`"},
		{"agy listing fails", Spec{"agy", "gemini-9", ""}, Env{Path: failing}, warn, "check that agy is logged in"},
		{"agy not installed", Spec{"agy", "gemini-9", ""}, Env{Path: t.TempDir()}, warn, "agy model gemini-9 could not be checked"},
		{"agy effort", Spec{"agy", "gemini-3.8-flash-low", "high"}, Env{Path: listed}, refuse, "agy names the effort in its model id"},
		{"agy empty model", Spec{"agy", "", ""}, Env{Path: listed}, refuse, "agy model"},
		{"agy model with a space", Spec{"agy", "gemini 3", ""}, Env{Path: listed}, refuse, "agy model"},

		{"opencode bare", Spec{Harness: "opencode"}, Env{}, ok, ""},
		{"opencode model", Spec{"opencode", "opencode/big-pickle", ""}, Env{}, refuse, "its own configuration"},
		{"opencode effort", Spec{"opencode", "", "high"}, Env{}, refuse, "its own configuration"},
	}
	for _, row := range rows {
		got, err := Check(row.spec, row.env)
		switch {
		case row.kind == refuse && (!errors.Is(err, state.ErrInvalid) || got != "" || !strings.Contains(err.Error(), row.text)):
			t.Errorf("%s: warn %q, err %v; want a refusal naming %q", row.name, got, err, row.text)
		case row.kind == warn && (err != nil || !strings.Contains(got, row.text) || !strings.HasSuffix(got, "starting anyway") || !strings.Contains(got, row.spec.Harness) || !strings.Contains(got, strings.TrimSpace(row.spec.Model))):
			t.Errorf("%s: warn %q, err %v; want a warning naming %q", row.name, got, err, row.text)
		case row.kind == ok && (err != nil || got != ""):
			t.Errorf("%s: warn %q, err %v; want a clean pass", row.name, got, err)
		}
	}
}

func TestCodexHome(t *testing.T) {
	env := map[string]string{"HOME": "/home/me"}
	getenv := func(k string) string { return env[k] }
	if got := CodexHome(getenv); got != filepath.Join("/home/me", ".codex") {
		t.Fatalf("default = %s", got)
	}
	env["CODEX_HOME"] = "/c"
	if got := CodexHome(getenv); got != "/c" {
		t.Fatalf("override = %s", got)
	}
}

func TestArgvIsExact(t *testing.T) {
	c, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, "fix it", "")
	if err != nil || !slices.Equal(c, []string{"/bin/claude", "--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "fix it"}) {
		t.Fatalf("claude argv = %q, %v", c, err)
	}
	for _, dir := range spacedDirs(t) {
		x, err := Argv("/bin/codex", Spec{"codex", "gpt-6-luna", "medium"}, "fix it", dir)
		if err != nil || !slices.Equal(x, []string{"/bin/codex", "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", dir, "-m", "gpt-6-luna", "-c", "model_reasoning_effort=medium", "fix it"}) {
			t.Fatalf("codex argv = %q, %v", x, err)
		}
	}
	if _, err := Argv("/bin/codex", Spec{"codex", "gpt-6-luna", "medium"}, "fix it", "relative/dir"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("relative dir err = %v", err)
	}
	for _, bad := range []string{"", "  ", "-p trick", "a\x00b", strings.Repeat("x", MaxPromptBytes+1)} {
		if _, err := Argv("/bin/claude", Spec{"claude", "sonnet", "low"}, bad, ""); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("prompt %.10q err = %v", bad, err)
		}
	}
}

func TestOpencodeStartsBare(t *testing.T) {
	if _, err := Check(Spec{Harness: "opencode"}, Env{CodexHome: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestOpencodeArgvPrefillsThePrompt(t *testing.T) {
	o, err := Argv("/usr/bin/opencode", Spec{Harness: "opencode"}, "fix it", "")
	if err != nil || !slices.Equal(o, []string{"/usr/bin/opencode", "--standalone", "--auto", "--prompt", "fix it"}) {
		t.Fatalf("opencode argv = %q, %v", o, err)
	}
	if _, err := Argv("/bin/gemini", Spec{Harness: "gemini"}, "fix it", ""); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("unknown harness argv err = %v", err)
	}
	if !Prefills("opencode") || Prefills("claude") || Prefills("codex") {
		t.Fatal("only opencode pre-fills its prompt")
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
	fakebin.Install(t, dir, "agy", "agy", map[string]string{"count": count, "models": models, "exit": code})
	return dir, count
}

func TestAgyArgvIsExact(t *testing.T) {
	got, err := Argv("/bin/agy", Spec{"agy", "gemini-3.8-flash-low", ""}, "brief", "")
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
	fakebin.Install(t, dir, "agy", "agy", map[string]string{"count": count, "stderr": "not signed in\n", "exit": "1"})
	for range 2 {
		if _, err := Models("agy", Env{Path: dir}); err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "check that agy is logged in") {
			t.Fatalf("err = %v", err)
		}
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "x") != 1 {
		t.Fatalf("agy models ran %q times, %v", b, err)
	}
}

func TestAgyModelsDoNotWaitForAChildHoldingStdout(t *testing.T) {
	dir := t.TempDir()
	fakebin.Install(t, dir, "agy", "agy", map[string]string{"models": agyList, "hold": "1", "holders": holders(t)})
	start := time.Now()
	m, err := Models("agy", Env{Path: dir})
	if took := time.Since(start); err != nil || len(m) != 2 || took > 3*time.Second {
		t.Fatalf("agy models = %v, %v after %v", m, err, took)
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
	fakebin.Install(t, path, "agy", "agy", map[string]string{"count": count, "models": agyList, "exit": "0"})
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

func TestAgyModelsTimeoutSaysTimedOut(t *testing.T) {
	defer func(d time.Duration) { agyTimeout = d }(agyTimeout)
	agyTimeout = 200 * time.Millisecond
	dir := t.TempDir()
	fakebin.Install(t, dir, "agy", "agy", map[string]string{"sleep": "5s"})
	_, err := Models("agy", Env{Path: dir})
	if !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "agy models timed out after 200ms") || strings.Contains(err.Error(), "logged in") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgyModelsSlowListingWithinTimeoutSucceeds(t *testing.T) {
	defer func(d time.Duration) { agyTimeout = d }(agyTimeout)
	agyTimeout = 3 * time.Second
	dir := t.TempDir()
	fakebin.Install(t, dir, "agy", "agy", map[string]string{"sleep": "1s", "models": agyList})
	if warn, err := Check(Spec{"agy", "gemini-3.1-pro-high", ""}, Env{Path: dir}); err != nil || warn != "" {
		t.Fatalf("slow listing = %q, %v", warn, err)
	}
}
