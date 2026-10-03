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
	fakebin.Main(map[string]func([]string) int{"agy": fakeAgyMain, "opencode": fakeOpencodeMain})
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
	if got := CodexHome(getenv); got != filepath.Join("/home/me", ".codex") {
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
	if err := Validate(Spec{"agy", "gemini-3.1-pro-high", ""}, Env{Path: dir}); err != nil {
		t.Fatalf("slow listing = %v", err)
	}
}
