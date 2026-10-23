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
	fakebin.Main(map[string]func([]string) int{"lister": fakeListerMain, "opencode": fakeOpencodeMain, "echoargs": echoArgsMain})
	os.Exit(m.Run())
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

func TestCheckDecidesBetweenAcceptWarnAndRefuse(t *testing.T) {
	cached := t.TempDir()
	if err := os.WriteFile(filepath.Join(cached, "models_cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "models_cache.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	agyListed := seeded(t, Listing{Harness: "agy", Models: []Model{{Name: "gemini-3.8-flash-low", Source: SourceDiscovered}, {Name: "gemini-3.1-pro-high", Source: SourceDiscovered}}, Gone: []string{"gemini-2"}})
	agyFailed := seeded(t, Listing{Harness: "agy", Failed: &Failure{Reason: "agy is not signed in; run agy and sign in"}})
	agyNone := seeded(t)
	codexListed := seeded(t, Listing{Harness: "codex", Models: []Model{
		{Name: "gpt-6-luna", Efforts: []string{"low", "medium"}, Source: SourceBuiltin},
		{Name: "codex-auto-review", Efforts: []string{"low"}, Source: SourceBuiltin, Hidden: true},
	}, Gone: []string{"gpt-5.4"}})
	claudeListed := seeded(t, Listing{Harness: "claude", Models: []Model{
		{Name: "opus", Efforts: claudeEfforts, Source: SourceBuiltin},
		{Name: "acme-opus", Efforts: claudeEfforts, Source: SourceCustom, Unverified: true},
		{Name: "claude-opus-4-6", Efforts: []string{"low", "medium", "high", "max"}, Source: SourceBuiltin},
	}, Gone: []string{"old-gw"}})
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
		{"codex unlisted", Spec{"codex", "acme-coder-1", "low"}, Env{CodexHome: cached}, warn, "codex model acme-coder-1 is not in codex's model list"},
		{"codex effort not listed", Spec{"codex", "gpt-5.5", "low"}, Env{CodexHome: cached}, warn, "codex model gpt-5.5 lists efforts high, not low"},
		{"codex without a cache", Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: t.TempDir()}, warn, "no model list yet; run `hand models --refresh codex`"},
		{"codex with a broken cache", Spec{"codex", "gpt-6-luna", "low"}, Env{CodexHome: broken}, warn, "no model list yet"},
		{"codex detected", Spec{"codex", "gpt-6-luna", "medium"}, codexListed, ok, ""},
		{"codex detected effort", Spec{"codex", "gpt-6-luna", "max"}, codexListed, warn, "lists efforts low, medium, not max"},
		{"codex hidden model", Spec{"codex", "codex-auto-review", "low"}, codexListed, ok, ""},
		{"codex vanished", Spec{"codex", "gpt-5.4", "low"}, codexListed, warn, "model gpt-5.4 is no longer listed by codex"},
		{"claude detected custom", Spec{"claude", "acme-opus", "max"}, claudeListed, ok, ""},
		{"claude detected effort", Spec{"claude", "claude-opus-4-6", "xhigh"}, claudeListed, warn, "effort xhigh is not one of low, medium, high, max"},
		{"claude vanished", Spec{"claude", "old-gw", "low"}, claudeListed, warn, "model old-gw is no longer listed by claude"},
		{"claude absent from the list", Spec{"claude", "acme-sonnet", "low"}, claudeListed, warn, "claude model acme-sonnet is not an alias"},
		{"codex empty model", Spec{"codex", "", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex empty effort", Spec{"codex", "gpt-6-luna", ""}, Env{CodexHome: cached}, refuse, "codex effort"},
		{"codex model with a space", Spec{"codex", "gpt 6", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex flag model", Spec{"codex", "--oss", "low"}, Env{CodexHome: cached}, refuse, "codex model"},
		{"codex effort with an equals sign", Spec{"codex", "gpt-6-luna", "low=1"}, Env{CodexHome: cached}, refuse, "codex effort"},
		{"codex custom effort", Spec{"codex", "gpt-6-luna", "ultra"}, Env{CodexHome: cached}, warn, "lists efforts low, medium, not ultra"},

		{"agy listed", Spec{"agy", "gemini-3.1-pro-high", ""}, agyListed, ok, ""},
		{"agy unlisted", Spec{"agy", "gemini-9", ""}, agyListed, warn, "agy model gemini-9 is not in `agy models`"},
		{"agy vanished", Spec{"agy", "gemini-2", ""}, agyListed, warn, "model gemini-2 is no longer listed by agy"},
		{"agy listing failed", Spec{"agy", "gemini-9", ""}, agyFailed, warn, "agy is not signed in"},
		{"agy no list yet", Spec{"agy", "gemini-9", ""}, agyNone, warn, "agy model gemini-9 could not be checked: no model list yet"},
		{"agy effort", Spec{"agy", "gemini-3.8-flash-low", "high"}, agyListed, refuse, "agy names the effort in its model id"},
		{"agy empty model", Spec{"agy", "", ""}, agyListed, refuse, "agy model"},
		{"agy model with a space", Spec{"agy", "gemini 3", ""}, agyListed, refuse, "agy model"},

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

func TestAgyArgvIsExact(t *testing.T) {
	got, err := Argv("/bin/agy", Spec{"agy", "gemini-3.8-flash-low", ""}, "brief", "")
	if want := []string{"/bin/agy", "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i", "brief"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("argv = %q, %v", got, err)
	}
}
