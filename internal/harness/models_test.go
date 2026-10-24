package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

const (
	secret  = "INVENTED-SECRET-DO-NOT-LEAK"
	cache   = `{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"gpt-5.5","supported_reasoning_levels":[{"effort":"high"}]}]}`
	agyList = "Fetching available models...\ngemini-3.8-flash-low\tGemini 3.8 Flash (Low)\ngemini-3.1-pro-high\tGemini 3.1 Pro (High)\n"
)

func fakeListerMain(args []string) int {
	p := fakebin.Params()
	if held(args) {
		return 0
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(p["version"])
		return 0
	}
	if p["count"] != "" {
		fakebin.Append(p["count"], "x")
	}
	if p["args"] != "" {
		fakebin.Append(p["args"], strings.Join(args, " "))
	}
	if p["stdin"] != "" {
		b, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(p["stdin"], b, 0o600)
	}
	if p["envfile"] != "" {
		_ = os.WriteFile(p["envfile"], []byte(strings.Join(os.Environ(), "\n")), 0o600)
	}
	if p["cwdfile"] != "" {
		wd, _ := os.Getwd()
		_ = os.WriteFile(p["cwdfile"], []byte(wd), 0o600)
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	if p["hold"] != "" {
		hold()
	}
	if n, _ := strconv.Atoi(p["big"]); n > 0 {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), n))
	}
	fmt.Fprint(os.Stderr, p["stderr"])
	fmt.Print(p["stdout"])
	code, _ := strconv.Atoi(p["exit"])
	return code
}

type fixture struct {
	t    *testing.T
	bin  string
	root string
	vars map[string]string
	env  Env
	opts Options
	mu   sync.Mutex
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, bin: t.TempDir(), root: t.TempDir(), now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	f.vars = map[string]string{"PATH": f.bin, "HOME": t.TempDir(), "SECONDHAND_HOME": f.root, "CODEX_HOME": t.TempDir()}
	f.env = EnvOf(func(k string) string { return f.vars[k] })
	environ := []string{"ANTHROPIC_DEFAULT_OPUS_MODEL=acme-opus", "HAND_TEST_SECRET=" + secret, "OPENCODE_CONFIG_CONTENT={}"}
	for k, v := range f.vars {
		environ = append(environ, k+"="+v)
	}
	if v := os.Getenv("SYSTEMROOT"); v != "" {
		environ = append(environ, "SYSTEMROOT="+v)
	}
	f.opts = Options{Environ: environ, Dir: t.TempDir(), Now: f.clock}
	return f
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fixture) install(name string, params map[string]string) string {
	f.t.Helper()
	if params["version"] == "" {
		params = mergeParams(params, map[string]string{"version": name + " 1.2.3"})
	}
	return fakebin.Install(f.t, f.bin, name, "lister", params)
}

func mergeParams(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{b, a} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func (f *fixture) refresh(name string) Listing {
	f.t.Helper()
	l, err := Refresh(context.Background(), name, f.env, f.opts)
	if err != nil {
		f.t.Fatalf("refresh %s: %v", name, err)
	}
	return l
}

func (f *fixture) cacheFile(name string) []byte {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "models", name+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func seeded(t *testing.T, listings ...Listing) Env {
	t.Helper()
	root := t.TempDir()
	if len(listings) > 0 {
		if err := os.MkdirAll(filepath.Join(root, "models"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range listings {
		l.Format = cacheFormat
		if err := store(filepath.Join(root, "models"), l); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{"SECONDHAND_HOME": root, "HOME": t.TempDir(), "PATH": t.TempDir()}
	return EnvOf(func(k string) string { return vars[k] })
}

func claudeStdout(t *testing.T, models ...map[string]any) string {
	t.Helper()
	reply := func(id string, models []map[string]any) string {
		b, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": id, "response": map[string]any{
				"models":   models,
				"account":  map[string]any{"email": secret + "@example.invalid", "organization": secret, "tokenSource": secret, "apiKeySource": secret},
				"commands": []map[string]any{{"name": "review", "description": secret}},
				"pid":      42,
			}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	other := []map[string]any{{"value": "wrong", "supportsEffort": true, "supportedEffortLevels": []string{"low"}}}
	return `{"type":"system","subtype":"init","apiKeySource":"` + secret + `"}` + "\nnot json at all " + secret + "\n" + reply("someone-else", other) + "\n" + reply(initializeID, models) + "\n"
}

func claudeRow(value, display string, levels ...string) map[string]any {
	row := map[string]any{"value": value, "displayName": display, "resolvedModel": value, "description": secret}
	if levels != nil {
		row["supportsEffort"], row["supportedEffortLevels"] = true, levels
	}
	return row
}

var all5 = []string{"low", "medium", "high", "xhigh", "max"}

func TestClaudeListsThroughTheInitializeRequestAndKeepsOnlyModels(t *testing.T) {
	f := newFixture(t)
	stdin, args, env, cwd := filepath.Join(t.TempDir(), "stdin"), filepath.Join(t.TempDir(), "args"), filepath.Join(t.TempDir(), "env"), filepath.Join(t.TempDir(), "cwd")
	f.install("claude", map[string]string{"stdin": stdin, "args": args, "envfile": env, "cwdfile": cwd, "stdout": claudeStdout(t,
		claudeRow("default", "Default (recommended)", all5...),
		claudeRow("opus[1m]", "Opus (1M context)", all5...),
		claudeRow("acme-opus", "acme-opus", all5...),
		claudeRow("my-gw/claude-x", "MyGW", "low", "high"),
		claudeRow("us.anthropic.claude-opus-4-6-v1", "Opus 4.6", "low", "medium", "high", "max"),
		claudeRow("haiku", "Haiku"),
	)})
	l := f.refresh("claude")
	if l.Failed != nil || l.Version != "claude 1.2.3" || l.Format != 1 || l.Harness != "claude" || !l.At.Equal(f.clock()) {
		t.Fatalf("listing = %+v", l)
	}
	want := []Model{
		{Name: "default", Label: "Default (recommended)", Efforts: all5, Source: SourceBuiltin},
		{Name: "opus[1m]", Label: "Opus (1M context)", Efforts: all5, Source: SourceBuiltin},
		{Name: "acme-opus", Label: "acme-opus", Efforts: all5, Source: SourceCustom, Unverified: true},
		{Name: "my-gw/claude-x", Label: "MyGW", Efforts: []string{"low", "high"}, Source: SourceCustom},
		{Name: "us.anthropic.claude-opus-4-6-v1", Label: "Opus 4.6", Efforts: []string{"low", "medium", "high", "max"}, Source: SourceBuiltin},
		{Name: "haiku", Label: "Haiku", Source: SourceBuiltin},
	}
	if len(l.Models) != len(want) {
		t.Fatalf("models = %+v", l.Models)
	}
	for i, m := range want {
		got := l.Models[i]
		if got.Name != m.Name || got.Label != m.Label || !slices.Equal(got.Efforts, m.Efforts) || got.Source != m.Source || got.Unverified != m.Unverified {
			t.Errorf("model %d = %+v, want %+v", i, got, m)
		}
	}
	if b, _ := os.ReadFile(stdin); string(b) != initializeRequest {
		t.Fatalf("stdin = %q", b)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "-p --input-format stream-json --output-format stream-json --verbose --no-session-persistence" {
		t.Fatalf("args = %q", b)
	}
	if b, _ := os.ReadFile(cwd); filepath.Clean(string(b)) != filepath.Clean(f.opts.Dir) && !strings.HasSuffix(filepath.Clean(string(b)), filepath.Base(f.opts.Dir)) {
		t.Fatalf("cwd = %q, want %q", b, f.opts.Dir)
	}
	childEnv, _ := os.ReadFile(env)
	for _, want := range []string{"PATH=", "HOME=", "ANTHROPIC_DEFAULT_OPUS_MODEL=acme-opus"} {
		if !strings.Contains(string(childEnv), want) {
			t.Errorf("child environment lacks %s: %q", want, childEnv)
		}
	}
	for _, bad := range []string{"HAND_TEST_SECRET", "OPENCODE_CONFIG_CONTENT", secret} {
		if strings.Contains(string(childEnv), bad) {
			t.Errorf("child environment holds %s", bad)
		}
	}
	noSecret(t, "claude cache", f.cacheFile("claude"))
	if b, _ := json.Marshal(l); bytes.Contains(b, []byte(secret)) {
		t.Fatalf("listing holds the secret: %s", b)
	}
}

func noSecret(t *testing.T, what string, b []byte) {
	t.Helper()
	for _, bad := range []string{secret, "example.invalid", "tokenSource", "organization", "account"} {
		if bytes.Contains(b, []byte(bad)) {
			t.Errorf("%s holds %q: %s", what, bad, b)
		}
	}
}

func TestClaudeListingFailuresGiveAReasonAndNoList(t *testing.T) {
	refused, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": initializeID, "error": secret}})
	for _, c := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"garbage", map[string]string{"stdout": "{" + secret + "\n[1,2\n"}, "claude gave no initialize response; it may be too old"},
		{"answer for another request only", map[string]string{"stdout": strings.Split(claudeStdout(t, claudeRow("opus", "Opus", all5...)), "\n")[2] + "\n"}, "no initialize response"},
		{"refused", map[string]string{"stdout": string(refused) + "\n"}, "claude refused the initialize request"},
		{"too old to know the flags", map[string]string{"exit": "2", "stderr": "error: unknown option " + secret}, "claude -p exited with code 2"},
		{"no models", map[string]string{"stdout": claudeStdout(t)}, "it listed no models"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.install("claude", c.params)
			l := f.refresh("claude")
			if l.Failed == nil || !strings.Contains(l.Failed.Reason, c.want) || len(l.Models) != 0 {
				t.Fatalf("listing = %+v, want reason %q", l, c.want)
			}
			noSecret(t, "claude cache", f.cacheFile("claude"))
			noSecret(t, "reason", []byte(l.Failed.Reason))
			if _, err := Detected("claude", f.env); err != nil {
				t.Fatalf("claude falls back to its aliases: %v", err)
			}
		})
	}
}

func TestClaudeOutputWithTheAnswerButANonZeroExitIsStillUsed(t *testing.T) {
	f := newFixture(t)
	f.install("claude", map[string]string{"exit": "1", "stdout": claudeStdout(t, claudeRow("opus", "Opus", all5...))})
	if l := f.refresh("claude"); l.Failed != nil || len(l.Models) != 1 {
		t.Fatalf("listing = %+v", l)
	}
}

func codexBundled(extra string) string {
	return `{"models":[` +
		`{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","base_instructions":"` + secret + `","supported_reasoning_levels":[{"effort":"low","description":"` + secret + `"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"}]},` +
		`{"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"ultra"}]},` +
		`{"slug":"codex-auto-review","display_name":"Auto review","visibility":"hide","supported_reasoning_levels":[{"effort":"low"}]}` + extra + `]}`
}

func TestCodexListsTheBundledCatalogAndHonoursVisibility(t *testing.T) {
	f := newFixture(t)
	args := filepath.Join(t.TempDir(), "args")
	f.install("codex", map[string]string{"args": args, "stdout": codexBundled(`,{"slug":"bad name","visibility":"list"}`)})
	l := f.refresh("codex")
	if l.Failed != nil || len(l.Models) != 3 || l.Models[0].Name != "gpt-6-luna" || l.Models[0].Label != "GPT-6-Luna" || l.Models[0].Source != SourceBuiltin || !slices.Equal(l.Models[0].Efforts, all5) || !slices.Equal(l.Models[1].Efforts, []string{"low", "ultra"}) {
		t.Fatalf("listing = %+v", l)
	}
	if !l.Models[2].Hidden || l.Models[0].Hidden {
		t.Fatalf("hidden = %+v", l.Models)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "debug models --bundled" {
		t.Fatalf("args = %q", b)
	}
	offered, err := Detected("codex", f.env)
	if err != nil || len(offered) != 2 {
		t.Fatalf("offered = %+v, %v", offered, err)
	}
	if warn, err := Check(Spec{"codex", "codex-auto-review", "low"}, f.env); err != nil || warn != "" {
		t.Fatalf("a hidden model is still known: %q, %v", warn, err)
	}
	noSecret(t, "codex cache", f.cacheFile("codex"))
}

func TestCodexFallsBackToItsOwnCacheAndNeverCopiesItsIdentity(t *testing.T) {
	for _, c := range []struct {
		name   string
		params map[string]string
	}{
		{"command fails", map[string]string{"exit": "2", "stderr": "unknown subcommand " + secret}},
		{"garbage output", map[string]string{"stdout": "{{" + secret}},
		{"no models in it", map[string]string{"stdout": `{"models":[]}`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.install("codex", c.params)
			own := `{"client_version":"0.163.0","etag":"` + secret + `","fetched_at":"2026-10-23T09:05:11Z","identity":"` + secret + `","models":[{"slug":"gpt-reserve","visibility":"hide","supported_reasoning_levels":[{"effort":"low"}]},{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]}]}`
			if err := os.WriteFile(filepath.Join(f.vars["CODEX_HOME"], "models_cache.json"), []byte(own), 0o600); err != nil {
				t.Fatal(err)
			}
			l := f.refresh("codex")
			if l.Failed != nil || len(l.Models) != 2 || l.Models[1].Source != SourceDiscovered || !l.Models[0].Hidden {
				t.Fatalf("listing = %+v", l)
			}
			noSecret(t, "codex cache", f.cacheFile("codex"))
		})
	}
}

func TestCodexFallbackNeverMarksAModelGone(t *testing.T) {
	f := newFixture(t)
	f.install("codex", map[string]string{"stdout": codexBundled(`,{"slug":"gpt-5.5","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}`)})
	f.refresh("codex")
	f.install("codex", map[string]string{"exit": "2"})
	own := `{"models":[{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`
	if err := os.WriteFile(filepath.Join(f.vars["CODEX_HOME"], "models_cache.json"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	l := f.refresh("codex")
	if l.Failed != nil || len(l.Models) != 1 || len(l.Gone) != 0 {
		t.Fatalf("listing = %+v", l)
	}
	if why, hard := Vanished(Spec{"codex", "gpt-5.5", "low"}, f.env, f.clock()); why != "" || hard {
		t.Fatalf("a narrower fallback list made a model vanish: %q %v", why, hard)
	}
	f.install("codex", map[string]string{"stdout": codexBundled("")})
	f.refresh("codex")
	f.install("codex", map[string]string{"stdout": codexLater2})
	if l := f.refresh("codex"); !slices.Contains(l.Gone, "gpt-6-luna") {
		t.Fatalf("a complete list stopped marking gone: %+v", l)
	}
	f.install("codex", map[string]string{"exit": "2"})
	back := `{"models":[{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`
	if err := os.WriteFile(filepath.Join(f.vars["CODEX_HOME"], "models_cache.json"), []byte(back), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := f.refresh("codex"); slices.Contains(l.Gone, "gpt-6-luna") || !slices.Contains(l.Gone, "codex-auto-review") {
		t.Fatalf("a listed model is still gone, or a missing one was forgotten: %+v", l.Gone)
	}
	if why, hard := Vanished(Spec{"codex", "gpt-6-luna", "low"}, f.env, f.clock()); why != "" || hard {
		t.Fatalf("a listed model vanished: %q %v", why, hard)
	}
	if warn, err := Check(Spec{"codex", "gpt-6-luna", "low"}, f.env); err != nil || warn != "" {
		t.Fatalf("a listed model warns: %q, %v", warn, err)
	}
}

func TestCodexWithNeitherSourceHasNoListAndAReason(t *testing.T) {
	f := newFixture(t)
	f.install("codex", map[string]string{"exit": "2"})
	l := f.refresh("codex")
	if l.Failed == nil || !strings.Contains(l.Failed.Reason, "codex debug models --bundled exited with code 2, and models_cache.json is unusable") {
		t.Fatalf("listing = %+v", l)
	}
	if _, err := Detected("codex", f.env); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "codex debug models") {
		t.Fatalf("detected = %v", err)
	}
}

func TestCodexColdStartReadsItsOwnCacheWithoutRunningAnything(t *testing.T) {
	f := newFixture(t)
	count := filepath.Join(t.TempDir(), "count")
	f.install("codex", map[string]string{"count": count})
	if err := os.WriteFile(filepath.Join(f.vars["CODEX_HOME"], "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Models("codex", f.env)
	if err != nil || len(got) != 2 || got[0].Name != "gpt-6-luna" || got[0].Source != SourceDiscovered {
		t.Fatalf("models = %+v, %v", got, err)
	}
	if _, err := os.Stat(count); err == nil {
		t.Fatal("a read ran codex")
	}
}

const codexLater2 = `{"models":[{"slug":"gpt-5.6-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`

func TestOpencodeListsNamesWithStandaloneOnly(t *testing.T) {
	f := newFixture(t)
	args := filepath.Join(t.TempDir(), "args")
	f.install("opencode", map[string]string{"args": args, "stderr": "apiKey=" + secret + "\n", "stdout": "acme/acme-coder-1\nopencode/big-pickle\n\nwarning: not a model name\n  acme/acme-small  \n"})
	l := f.refresh("opencode")
	if l.Failed != nil || len(l.Models) != 3 || l.Models[0].Name != "acme/acme-coder-1" || l.Models[2].Name != "acme/acme-small" || l.Models[0].Source != SourceDiscovered || len(l.Models[0].Efforts) != 0 {
		t.Fatalf("listing = %+v", l)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "models --standalone" {
		t.Fatalf("args = %q", b)
	}
	noSecret(t, "opencode cache", f.cacheFile("opencode"))
	if got, err := Models("opencode", f.env); err != nil || got != nil {
		t.Fatalf("opencode offers no model until one can be applied: %+v, %v", got, err)
	}
	if got, err := Detected("opencode", f.env); err != nil || len(got) != 3 {
		t.Fatalf("detected = %+v, %v", got, err)
	}
}

func TestOpencodeFailuresNameNoStderr(t *testing.T) {
	for _, c := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"exit", map[string]string{"exit": "1", "stderr": "boom " + secret}, "opencode models --standalone exited with code 1"},
		{"no names", map[string]string{"stdout": "{}\nsomething else\n"}, "opencode models --standalone printed no model names"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.install("opencode", c.params)
			l := f.refresh("opencode")
			if l.Failed == nil || l.Failed.Reason != c.want {
				t.Fatalf("listing = %+v", l)
			}
			noSecret(t, "opencode cache", f.cacheFile("opencode"))
		})
	}
}

func TestAgyListsIdsAndLabels(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList, "stderr": "Fetching available models...\n"})
	l := f.refresh("agy")
	if l.Failed != nil || len(l.Models) != 2 || l.Models[0].Name != "gemini-3.8-flash-low" || l.Models[0].Label != "Gemini 3.8 Flash (Low)" || l.Models[0].Source != SourceDiscovered || l.Models[0].Efforts != nil {
		t.Fatalf("listing = %+v", l)
	}
}

func TestTheVersionIsOneCleanShortLine(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList, "version": "agy \x1b[31m1.4.4\x1b[0m " + strings.Repeat("v", 200) + "\nsecond line " + secret})
	l := f.refresh("agy")
	if len([]rune(l.Version)) != 64 || strings.ContainsAny(l.Version, "\x1b\n") || strings.Contains(l.Version, secret) || !strings.HasPrefix(l.Version, "agy [31m1.4.4") {
		t.Fatalf("version = %q", l.Version)
	}
}

func TestAgyFailuresAreShortAndNeverRawStderr(t *testing.T) {
	for _, c := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"signed out", map[string]string{"exit": "1", "stderr": "Fetching available models...\nError: Please sign in to view available models. " + secret + "\n"}, "agy is not signed in; run agy and sign in"},
		{"other failure", map[string]string{"exit": "1", "stderr": "kaboom " + secret}, "agy models exited with code 1"},
		{"no models", map[string]string{"stdout": "Fetching available models...\n"}, "it listed no models"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.install("agy", c.params)
			l := f.refresh("agy")
			if l.Failed == nil || !strings.Contains(l.Failed.Reason, c.want) {
				t.Fatalf("listing = %+v", l)
			}
			noSecret(t, "agy cache", f.cacheFile("agy"))
		})
	}
}

func TestAMissingHarnessIsNoListAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	l, err := Refresh(context.Background(), "agy", f.env, f.opts)
	if err != nil || l.Failed == nil || l.Failed.Reason != "agy not found on PATH" || len(l.Models) != 0 {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "models")); err == nil {
		t.Fatal("a missing harness left a cache folder")
	}
	if st := StatusOf("agy", f.env, f.clock()); st.Missing != "agy not found on PATH" || st.Due {
		t.Fatalf("status = %+v", st)
	}
}

func TestRunnerBoundsTimeAndOutputAndKillsWhatItStarted(t *testing.T) {
	defer func(a, b time.Duration, c int) { agyTimeout, claudeTimeout, maxOutput = a, b, c }(agyTimeout, claudeTimeout, maxOutput)
	agyTimeout, maxOutput = 300*time.Millisecond, 1024
	t.Run("timeout", func(t *testing.T) {
		f := newFixture(t)
		f.install("agy", map[string]string{"sleep": "5s", "stdout": agyList})
		start := time.Now()
		l := f.refresh("agy")
		if l.Failed == nil || l.Failed.Reason != "agy models timed out after 300ms" || time.Since(start) > 4*time.Second {
			t.Fatalf("listing = %+v after %v", l, time.Since(start))
		}
	})
	t.Run("oversize", func(t *testing.T) {
		f := newFixture(t)
		f.install("agy", map[string]string{"big": "5000"})
		l := f.refresh("agy")
		if l.Failed == nil || !strings.Contains(l.Failed.Reason, "printed more than 1024 bytes") {
			t.Fatalf("listing = %+v", l)
		}
	})
	t.Run("a child holding stdout does not hold the refresh", func(t *testing.T) {
		agyTimeout = 30 * time.Second
		f := newFixture(t)
		f.install("agy", map[string]string{"stdout": agyList, "hold": "1", "holders": holders(t)})
		start := time.Now()
		if l := f.refresh("agy"); l.Failed != nil || len(l.Models) != 2 || time.Since(start) > 3*time.Second {
			t.Fatalf("listing = %+v after %v", l, time.Since(start))
		}
	})
}

func TestARefreshCancelledByItsContextRecordsNothing(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"sleep": "5s", "stdout": agyList})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Refresh(ctx, "agy", f.env, f.opts); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "models", "agy.json")); err == nil {
		t.Fatal("a cancelled refresh wrote the cache")
	}
}

func TestCacheFileIsAtomicPrivateAndNameOnly(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList})
	if err := os.MkdirAll(filepath.Join(f.root, "models"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "models", "agy.123.tmp"), []byte("left by a crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.refresh("agy")
	dir := filepath.Join(f.root, "models")
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, de := range des {
		left = append(left, de.Name())
	}
	slices.Sort(left)
	if !slices.Equal(left, []string{"agy.json", "agy.lock"}) {
		t.Fatalf("cache folder holds %v", left)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "agy.json"): 0o600, filepath.Join(dir, "agy.lock"): 0o600} {
			if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
				t.Fatalf("%s mode = %v, %v; want %v", filepath.Base(path), fi.Mode().Perm(), err, want)
			}
		}
	}
	var got map[string]any
	if err := json.Unmarshal(f.cacheFile("agy"), &got); err != nil {
		t.Fatal(err)
	}
	if got["format"] != float64(1) || got["harness"] != "agy" || got["version"] != "agy 1.2.3" || got["key"] == "" || got["at"] == nil {
		t.Fatalf("header = %v", got)
	}
	for _, bad := range []string{"/", `\\`} {
		if key, _ := got["key"].(string); strings.Contains(key, bad) {
			t.Fatalf("the key holds a path: %q", key)
		}
	}
}

func TestAnExistingLooseCacheFolderIsTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList})
	dir := filepath.Join(f.root, "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.refresh("agy")
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, %v", fi.Mode().Perm(), err)
	}
}

func TestCorruptOrForeignCacheFilesCountAsNoCache(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.root, "models")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{", `{"format":2,"harness":"agy","models":[{"name":"x"}]}`, `{"format":1,"harness":"codex","models":[{"name":"x"}]}`} {
		if err := os.WriteFile(filepath.Join(dir, "agy.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Detected("agy", f.env); err == nil || !strings.Contains(err.Error(), "no model list yet") {
			t.Fatalf("%s: err = %v", body, err)
		}
	}
}

func TestStaleByTimeByBinaryAndByConfigFile(t *testing.T) {
	f := newFixture(t)
	bin := f.install("claude", map[string]string{"stdout": claudeStdout(t, claudeRow("opus", "Opus", all5...))})
	f.refresh("claude")
	if st := StatusOf("claude", f.env, f.clock()); !st.Found || st.Stale || st.Due {
		t.Fatalf("fresh = %+v", st)
	}
	f.advance(59 * time.Minute)
	if st := StatusOf("claude", f.env, f.clock()); st.Stale {
		t.Fatalf("stale after 59 minutes: %+v", st)
	}
	f.advance(2 * time.Minute)
	if st := StatusOf("claude", f.env, f.clock()); !st.Stale || !st.Due || !st.Old {
		t.Fatalf("claude is stale after an hour: %+v", st)
	}
	f.refresh("claude")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(bin, later, later); err != nil {
		t.Fatal(err)
	}
	if st := StatusOf("claude", f.env, f.clock()); !st.Stale || !st.Due || st.Old {
		t.Fatalf("a changed binary leaves it fresh, or makes it old: %+v", st)
	}
	f.refresh("claude")
	dir := filepath.Join(f.vars["HOME"], ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"acme-opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := StatusOf("claude", f.env, f.clock()); !st.Stale {
		t.Fatalf("a new settings file leaves it fresh: %+v", st)
	}
	f.refresh("claude")
	if st := StatusOf("claude", f.env, f.clock()); st.Stale {
		t.Fatalf("refresh did not take the new key: %+v", st)
	}
}

func TestHoursPerHarness(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList})
	f.refresh("agy")
	f.advance(6*time.Hour - time.Minute)
	if st := StatusOf("agy", f.env, f.clock()); st.Stale {
		t.Fatalf("agy stale before 6 hours: %+v", st)
	}
	f.advance(2 * time.Minute)
	if st := StatusOf("agy", f.env, f.clock()); !st.Stale {
		t.Fatalf("agy fresh after 6 hours: %+v", st)
	}
}

func TestAFailedRefreshKeepsThePreviousListAndRecordsTheReason(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"stdout": agyList})
	f.refresh("agy")
	f.advance(7 * time.Hour)
	f.install("agy", map[string]string{"exit": "1", "stderr": "Please sign in " + secret})
	l := f.refresh("agy")
	if l.Failed == nil || l.Failed.Reason != "agy is not signed in; run agy and sign in" || len(l.Models) != 2 || l.Version != "agy 1.2.3" {
		t.Fatalf("listing = %+v", l)
	}
	got, err := Detected("agy", f.env)
	if err != nil || len(got) != 2 {
		t.Fatalf("detected = %+v, %v", got, err)
	}
	st := StatusOf("agy", f.env, f.clock())
	if !st.Stale || st.Due || st.Listing.Failed == nil {
		t.Fatalf("a failure is retried only after a while: %+v", st)
	}
	f.advance(14 * time.Minute)
	if st := StatusOf("agy", f.env, f.clock()); st.Due {
		t.Fatalf("due after 14 minutes: %+v", st)
	}
	f.advance(2 * time.Minute)
	if st := StatusOf("agy", f.env, f.clock()); !st.Due {
		t.Fatalf("not due after 16 minutes: %+v", st)
	}
	f.install("agy", map[string]string{"stdout": agyList})
	if l := f.refresh("agy"); l.Failed != nil {
		t.Fatalf("a good refresh keeps the failure: %+v", l)
	}
	noSecret(t, "agy cache", f.cacheFile("agy"))
}

func TestAFailureWithNoPreviousListIsRecordedSoItIsNotRetriedAtOnce(t *testing.T) {
	f := newFixture(t)
	f.install("agy", map[string]string{"exit": "1"})
	f.refresh("agy")
	if st := StatusOf("agy", f.env, f.clock()); !st.Found || !st.Stale || st.Due || len(st.Listing.Models) != 0 {
		t.Fatalf("status = %+v", st)
	}
	if _, err := Detected("agy", f.env); err == nil || !strings.Contains(err.Error(), "agy models exited with code 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestTwoRefreshersRunOneCommand(t *testing.T) {
	f := newFixture(t)
	count := filepath.Join(t.TempDir(), "count")
	f.install("agy", map[string]string{"stdout": agyList, "count": count, "sleep": "600ms"})
	var wg sync.WaitGroup
	results := make([]Listing, 2)
	run := func(i int) {
		defer wg.Done()
		l, err := Refresh(context.Background(), "agy", f.env, f.opts)
		if err != nil {
			t.Error(err)
		}
		results[i] = l
	}
	wg.Add(2)
	go run(0)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(count); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("the first refresher never ran the command")
		}
	}
	go run(1)
	wg.Wait()
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "x") != 1 {
		t.Fatalf("agy ran %q times, %v", b, err)
	}
	if len(results[0].Models) != 2 || len(results[1].Models) != 2 {
		t.Fatalf("results = %+v", results)
	}
}

func TestSequentialRefreshesEachRun(t *testing.T) {
	f := newFixture(t)
	count := filepath.Join(t.TempDir(), "count")
	f.install("agy", map[string]string{"stdout": agyList, "count": count})
	f.refresh("agy")
	f.refresh("agy")
	if b, _ := os.ReadFile(count); strings.Count(string(b), "x") != 2 {
		t.Fatalf("agy ran %q times", b)
	}
}

func TestModelsThatLeftTheListAreRemembered(t *testing.T) {
	f := newFixture(t)
	f.install("codex", map[string]string{"stdout": codexBundled("")})
	f.refresh("codex")
	if why, hard := Vanished(Spec{"codex", "gpt-6-luna", "low"}, f.env, f.clock()); why != "" || hard {
		t.Fatalf("a listed model vanished: %q %v", why, hard)
	}
	f.install("codex", map[string]string{"stdout": `{"models":[{"slug":"gpt-5.6-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`})
	f.refresh("codex")
	spec := Spec{"codex", "gpt-6-luna", "low"}
	if why, hard := Vanished(spec, f.env, f.clock()); why != "model gpt-6-luna is no longer listed by codex" || !hard {
		t.Fatalf("vanished = %q %v", why, hard)
	}
	if why, _ := Vanished(Spec{"codex", "never-listed", "low"}, f.env, f.clock()); why != "" {
		t.Fatalf("a model that was never listed vanished: %q", why)
	}
	f.advance(7 * time.Hour)
	if why, hard := Vanished(spec, f.env, f.clock()); why == "" || hard {
		t.Fatalf("a stale list is authoritative: %q %v", why, hard)
	}
	f.install("codex", map[string]string{"stdout": codexBundled("")})
	f.refresh("codex")
	if why, _ := Vanished(spec, f.env, f.clock()); why != "" {
		t.Fatalf("a model that came back stays gone: %q", why)
	}
}

func TestClaudeVanishingIsNeverAuthoritative(t *testing.T) {
	f := newFixture(t)
	f.install("claude", map[string]string{"stdout": claudeStdout(t, claudeRow("acme-opus", "Acme", all5...), claudeRow("opus", "Opus", all5...))})
	f.refresh("claude")
	f.install("claude", map[string]string{"stdout": claudeStdout(t, claudeRow("opus", "Opus", all5...))})
	f.refresh("claude")
	if why, hard := Vanished(Spec{"claude", "acme-opus", "low"}, f.env, f.clock()); why != "model acme-opus is no longer listed by claude" || hard {
		t.Fatalf("vanished = %q %v", why, hard)
	}
	if warn, err := Check(Spec{"claude", "acme-opus", "low"}, f.env); err != nil || !strings.Contains(warn, "model acme-opus is no longer listed by claude") {
		t.Fatalf("warn = %q, %v", warn, err)
	}
}

func TestGoneIsBoundedAndKeepsTheNewest(t *testing.T) {
	var prev Listing
	for i := range maxGone + 50 {
		prev.Models = append(prev.Models, Model{Name: "m" + strconv.Itoa(i)})
	}
	out := gone(prev, nil, true)
	if len(out) != maxGone || out[len(out)-1] != "m"+strconv.Itoa(maxGone+49) {
		t.Fatalf("gone = %d, last %q", len(out), out[len(out)-1])
	}
}

func TestReadsNeverRunAHarness(t *testing.T) {
	f := newFixture(t)
	count := filepath.Join(t.TempDir(), "count")
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		f.install(name, map[string]string{"count": count, "stdout": agyList})
	}
	for _, spec := range []Spec{{"claude", "acme", "low"}, {"codex", "gpt-6-luna", "low"}, {"agy", "gemini-3.8-flash-low", ""}, {Harness: "opencode"}} {
		if _, err := Check(spec, f.env); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		_, _ = Models(name, f.env)
		_, _ = Detected(name, f.env)
		StatusOf(name, f.env, f.clock())
		Vanished(Spec{Harness: name, Model: "x"}, f.env, f.clock())
	}
	if _, err := os.Stat(count); err == nil {
		t.Fatal("a read ran a harness")
	}
}

func TestColdStartAndUnknownHarness(t *testing.T) {
	env := seeded(t)
	claude, err := Models("claude", env)
	if err != nil || len(claude) != 4 || claude[0].Name != "opus" || claude[0].Source != SourceBuiltin || !slices.Equal(claude[3].Efforts, claudeEfforts) {
		t.Fatalf("claude = %+v, %v", claude, err)
	}
	if _, err := Models("agy", env); err == nil || !strings.Contains(err.Error(), "no model list yet; run `hand models --refresh agy`") {
		t.Fatalf("agy = %v", err)
	}
	for _, call := range []func() error{
		func() error { _, err := Models("gemini", env); return err },
		func() error { _, err := Detected("gemini", env); return err },
		func() error { _, err := Refresh(context.Background(), "gemini", env, Options{}); return err },
	} {
		if err := call(); !errors.Is(err, state.ErrInvalid) {
			t.Fatalf("unknown harness err = %v", err)
		}
	}
}

func TestRefreshWithoutAFolderForTheCacheSaysSo(t *testing.T) {
	env := EnvOf(func(k string) string { return map[string]string{"PATH": t.TempDir()}[k] })
	if _, err := Refresh(context.Background(), "agy", env, Options{}); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "SECONDHAND_HOME") {
		t.Fatalf("err = %v", err)
	}
}

func TestChildEnvironmentKeepsOnlyPathHomesAndTheHarnessesOwn(t *testing.T) {
	environ := []string{"Path=C:\\bin", "HOME=/h", "AWS_SECRET_ACCESS_KEY=" + secret, "ANTHROPIC_BASE_URL=http://gw.invalid", "CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1", "GITHUB_TOKEN=" + secret, "CODEX_HOME=/c", "OPENCODE_CONFIG_CONTENT={}"}
	got := childEnv(environ, claudeLister.prefixes)
	want := []string{"Path=C:\\bin", "HOME=/h", "ANTHROPIC_BASE_URL=http://gw.invalid", "CLAUDE_CODE_USE_BEDROCK=1", "AWS_REGION=us-east-1"}
	if !slices.Equal(got, want) {
		t.Fatalf("claude env = %q, want %q", got, want)
	}
	if got := childEnv(environ, codexLister.prefixes); !slices.Equal(got, []string{"Path=C:\\bin", "HOME=/h", "CODEX_HOME=/c"}) {
		t.Fatalf("codex env = %q", got)
	}
	if got := childEnv(environ, agyLister.prefixes); !slices.Equal(got, []string{"Path=C:\\bin", "HOME=/h"}) {
		t.Fatalf("agy env = %q", got)
	}
}

func TestListingTextIsCleanedBeforeItIsCached(t *testing.T) {
	long := strings.Repeat("é", 300)
	out := tidy([]Model{
		{Name: "ok", Label: "a\x1b[31mb\nc\td" + long, Efforts: []string{"low", "bad effort", "low", "high"}, Source: SourceDiscovered},
		{Name: "ok", Source: SourceDiscovered},
		{Name: "-flag"},
		{Name: "has space"},
		{Name: ""},
	})
	if len(out) != 1 || out[0].Name != "ok" || !slices.Equal(out[0].Efforts, []string{"low", "high"}) || len([]rune(out[0].Label)) != 100 || strings.ContainsAny(out[0].Label, "\x1b\n\t") {
		t.Fatalf("tidy = %+v", out)
	}
}
