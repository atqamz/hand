package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

const invented = "INVENTED-SECRET-DO-NOT-LEAK"

const (
	codexBundled = `{"models":[{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","base_instructions":"` + invented + `","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"codex-auto-review","visibility":"hide","supported_reasoning_levels":[{"effort":"low"}]}]}`
	codexLater   = `{"models":[{"slug":"gpt-5.6-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`
)

func listers(t *testing.T, h *harness, log string) string {
	t.Helper()
	dir := fakeBin(t)
	install := func(name, stdout string, extra map[string]string) {
		params := map[string]string{"version": name + " 9.9", "stdout": stdout, "log": log}
		for k, v := range extra {
			params[k] = v
		}
		fakebin.Install(t, dir, name, "lister", params)
	}
	install("claude", claudeAnswer, nil)
	install("codex", codexBundled, nil)
	install("opencode", "acme/acme-coder-1\nopencode/big-pickle\n", map[string]string{"stderr": "apiKey=" + invented})
	install("agy", "Fetching...\ngemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n", nil)
	h.vars["PATH"] = dir
	h.vars["HOME"] = t.TempDir()
	h.vars["CODEX_HOME"] = t.TempDir()
	return dir
}

func TestModelsPrintsTheListPerHarnessWithoutAFleet(t *testing.T) {
	h := newHarness(t)
	listers(t, h, "")
	cold := h.ok("models")
	has(t, "cold models", cold, "claude,opus,\"\",low medium high xhigh max,built-in", "listings[4]{harness,version,age,state}:", "claude,\"\",\"\",no list yet; run `hand models --refresh claude`", "opencode,\"\",\"\",no list yet; run `hand models --refresh opencode`")
	if strings.Contains(cold, "agy,gemini") {
		t.Fatalf("cold models ran agy: %q", cold)
	}
	out := h.ok("models", "--refresh")
	has(t, "models", out,
		"models[6]{harness,model,label,efforts,source}:",
		"claude,opus,Opus,low medium high xhigh max,built-in",
		"claude,acme-opus,Acme,low medium high xhigh max (unverified),custom",
		"codex,gpt-6-luna,GPT-6-Luna,low medium,built-in",
		"opencode,acme/acme-coder-1,\"\",\"\",discovered",
		"agy,gemini-3.8-flash-low,Gemini 3.8 Flash (Low),\"\",discovered",
		"listings[4]{harness,version,age,state}:",
		"claude,claude 9.9,<1m,ok",
		"codex,codex 9.9,<1m,ok",
		"opencode,opencode 9.9,<1m,ok",
		"agy,agy 9.9,<1m,ok")
	if strings.Contains(out, "codex-auto-review") || strings.Contains(out, "Run each harness") {
		t.Fatalf("a hidden model is offered, or the refresh hint stayed: %q", out)
	}
	has(t, "plain models", h.ok("models"), "Run each harness's listing command now: `hand models --refresh [HARNESS]`", "claude,claude 9.9,<1m,ok")
	h.advance(7 * time.Hour)
	has(t, "stale models", h.ok("models", "agy"), "agy,agy 9.9,7h,stale")
}

func TestModelsOneHarnessAndUsage(t *testing.T) {
	h := newHarness(t)
	listers(t, h, "")
	out := h.ok("models", "--refresh", "agy")
	has(t, "agy only", out, "models[1]{harness,model,label,efforts,source}:", "listings[1]{harness,version,age,state}:")
	for _, c := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"models", "gemini"}, `harness "gemini" must be one of claude, codex, opencode, agy`, 2},
		{[]string{"models", "agy", "codex"}, "want at most 1 argument", 2},
		{[]string{"models", "--bogus"}, "models:", 2},
	} {
		if _, errOut, code := h.run(c.args...); code != c.code || !strings.Contains(errOut, c.want) {
			t.Fatalf("%q: code=%d stderr=%q", c.args, code, errOut)
		}
	}
}

func TestModelsRefreshFailureIsAStateNotAnError(t *testing.T) {
	h := newHarness(t)
	dir := listers(t, h, "")
	fakebin.Install(t, dir, "claude", "lister", map[string]string{"stdout": "{" + invented + "\n", "version": "claude 1"})
	fakebin.Install(t, dir, "agy", "lister", map[string]string{"exit": "1", "stderr": "Error: Please sign in " + invented})
	if err := os.Remove(exe(dir, "opencode")); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := h.run("models", "--refresh")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "models", out,
		"claude,claude 1,\"\",\"no list: claude gave no initialize response; it may be too old\"",
		"opencode,\"\",\"\",opencode not found on PATH",
		"agy,\"\",\"\",\"no list: agy is not signed in; run agy and sign in\"")
	if strings.Contains(out+errOut, invented) {
		t.Fatalf("output holds the secret: %q %q", out, errOut)
	}
}

func TestModelsNeverPrintOrCacheASecret(t *testing.T) {
	h := newHarness(t)
	listers(t, h, "")
	h.ok("init")
	out := h.ok("models", "--refresh")
	for _, text := range []string{out, h.ok("models"), h.ok("route", "list")} {
		if strings.Contains(text, invented) {
			t.Fatalf("output holds the secret: %q", text)
		}
	}
	files, err := filepath.Glob(filepath.Join(h.vars["SECONDHAND_HOME"], "models", "*"))
	if err != nil || len(files) != 8 {
		t.Fatalf("cache files = %v, %v", files, err)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err != nil || strings.Contains(string(b), invented) || strings.Contains(string(b), "INVENTED") {
			t.Fatalf("%s holds the secret: %q, %v", f, b, err)
		}
	}
}

func TestRouteListRefreshesOnlyWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	log := filepath.Join(t.TempDir(), "log")
	listers(t, h, log)
	h.ok("init")
	first := h.ok("route", "list")
	has(t, "route list", first, "agy,gemini-3.8-flash-low,\"\"", "codex,gpt-6-luna,low medium", "claude,acme-opus,low medium high xhigh max")
	ran := func() []string {
		b, _ := os.ReadFile(log)
		lines := strings.Fields(string(b))
		slices.Sort(lines)
		return lines
	}
	if got := ran(); len(got) == 0 {
		t.Fatal("route list did not list the harnesses with no cache")
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	h.advance(24 * time.Hour)
	h.ok("route", "list")
	if got := ran(); len(got) != 0 {
		t.Fatalf("route list refreshed a stale cache: %q", got)
	}
}

func TestRouteListMarksAModelThatLeftTheList(t *testing.T) {
	h := newHarness(t)
	dir := listers(t, h, "")
	h.ok("init")
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"gone-codex":{"harness":"codex","model":"gpt-6-luna","effort":"low"},"gone-claude":{"harness":"claude","model":"acme-opus","effort":"low"},"here":{"harness":"codex","model":"gpt-5.6-sol","effort":"low"}}}`
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("models", "--refresh")
	fakebin.Install(t, dir, "codex", "lister", map[string]string{"stdout": codexLater, "version": "codex 9.9"})
	fakebin.Install(t, dir, "claude", "lister", map[string]string{"stdout": strings.ReplaceAll(claudeAnswer, `,{"value":"acme-opus","displayName":"Acme","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]}`, ""), "version": "claude 9.9"})
	h.ok("models", "--refresh", "codex")
	h.ok("models", "--refresh", "claude")
	list := h.ok("route", "list")
	has(t, "route list", list,
		`gone-codex,codex,gpt-6-luna,low,no,model gpt-6-luna is no longer listed by codex`,
		`gone-claude,claude,acme-opus,low,yes,model acme-opus is no longer listed by claude; starting anyway`,
		`here,codex,gpt-5.6-sol,low,yes,""`)
	h.advance(7 * time.Hour)
	has(t, "stale route list", h.ok("route", "list"), `gone-codex,codex,gpt-6-luna,low,yes,model gpt-6-luna is no longer listed by codex; starting anyway`)
}

func TestNoRequestPathRunsAHarness(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	log := filepath.Join(t.TempDir(), "log")
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		fakebin.Install(t, h.vars["PATH"], name, "lister", map[string]string{"log": log, "stdout": "gemini-3.8-flash-low\tG\n"})
	}
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(`{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"gem":{"harness":"agy","model":"gemini-9"},"cx":{"harness":"codex","model":"gpt-6-luna","effort":"low"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("supervisor", "start", "--harness", "agy", "--model", "gemini-9")
	h.ok("supervisor", "stop")
	h.ok("supervisor", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high")
	h.ok("supervisor", "stop")
	h.run("doctor")
	h.ok("models")
	if b, err := os.ReadFile(log); err == nil {
		t.Fatalf("a request path ran a harness: %q", b)
	}
	h.ok("models", "--refresh", "agy")
	if b, err := os.ReadFile(log); err != nil || strings.TrimSpace(string(b)) != "models" {
		t.Fatalf("the refresh did not run agy models: %q, %v", b, err)
	}
}

func loggedRuns(t *testing.T, log string) int {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}

func TestWatchRefreshesStaleModelListsOnEveryTick(t *testing.T) {
	fx := newAttemptFixture(t)
	for _, name := range []string{"claude", "codex", "opencode"} {
		if err := os.Remove(exe(fx.h.vars["PATH"], name)); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(t.TempDir(), "log")
	fakebin.Install(t, fx.h.vars["PATH"], "agy", "lister", map[string]string{"stdout": "gemini-3.8-flash-low\tG\n", "log": log, "version": "agy 1"})
	listed := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); loggedRuns(t, log) != n; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("agy was listed %d times, want %d", loggedRuns(t, log), n)
			}
		}
	}
	stop := startWatch(t, fx, "--every", "50ms")
	listed(1)
	written := filepath.Join(fx.h.vars["SECONDHAND_HOME"], "models", "agy.json")
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(written); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("the first listing never wrote the cache")
		}
	}
	fx.h.advance(7 * time.Hour)
	listed(2)
	stop()
}
