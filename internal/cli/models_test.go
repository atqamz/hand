package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

const invented = "INVENTED-SECRET-DO-NOT-LEAK"

const (
	codexBundled = `{"models":[{"slug":"gpt-6-luna","display_name":"GPT-6-Luna","visibility":"list","base_instructions":"` + invented + `","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},{"slug":"codex-auto-review","visibility":"hide","supported_reasoning_levels":[{"effort":"low"}]}]}`
	copilotHelp  = "  `model`: AI model to use for Copilot CLI\n    - \"gpt-6-luna\"\n    - \"claude-sonnet-5.5\"\n\n  `contextTier`: x\n"
	codexLater   = `{"models":[{"slug":"gpt-5.6-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}]}`
	ompModels    = `{"models":[{"provider":"commandcode","kind":"chat","id":"deepseek/deepseek-v4.1-flash","selector":"commandcode/deepseek/deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","reasoning":true,"thinking":["low","high","max"]},{"provider":"commandcode","kind":"chat","id":"plain","selector":"commandcode/plain","name":"Plain","reasoning":false,"thinking":null}]}`
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
	install("copilot", copilotHelp, nil)
	install("omp", ompModels, nil)
	h.vars["PATH"] = dir
	h.vars["HOME"] = t.TempDir()
	h.vars["CODEX_HOME"] = t.TempDir()
	return dir
}

func TestModelsPrintsTheListPerHarnessWithoutAFleet(t *testing.T) {
	h := newHarness(t)
	listers(t, h, "")
	cold := h.ok("models")
	has(t, "cold models", cold, "claude,opus,\"\",low medium high xhigh max,built-in", "listings[6]{harness,version,age,state}:", "claude,\"\",\"\",no list yet; run `hand models --refresh claude`", "opencode,\"\",\"\",no list yet; run `hand models --refresh opencode`", "omp,\"\",\"\",no list yet; run `hand models --refresh omp`")
	if strings.Contains(cold, "agy,gemini") {
		t.Fatalf("cold models ran agy: %q", cold)
	}
	out := h.ok("models", "--refresh")
	has(t, "models", out,
		"models[11]{harness,model,label,efforts,source}:",
		"claude,opus,Opus,low medium high xhigh max,built-in",
		"claude,acme-opus,Acme,low medium high xhigh max (unverified),custom",
		"codex,gpt-6-luna,GPT-6-Luna,low medium,built-in",
		"opencode,acme/acme-coder-1,\"\",\"\",discovered",
		"agy,gemini-3.8-flash-low,Gemini 3.8 Flash (Low),\"\",discovered",
		"copilot,auto,\"\",\"\",discovered",
		"copilot,gpt-6-luna,\"\",none minimal low medium high xhigh max (unverified),discovered",
		"omp,commandcode/deepseek/deepseek-v4.1-flash,DeepSeek V4.1 Flash,low high max,discovered",
		"omp,commandcode/plain,Plain,\"\",discovered",
		"listings[6]{harness,version,age,state}:",
		"claude,claude 9.9,<1m,ok",
		"codex,codex 9.9,<1m,ok",
		"opencode,opencode 9.9,<1m,ok",
		"agy,agy 9.9,<1m,ok",
		"copilot,copilot 9.9,<1m,ok",
		"omp,omp 9.9,<1m,ok")
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
		{[]string{"models", "gemini"}, `harness "gemini" must be one of claude, codex, opencode, agy, copilot, omp`, 2},
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
	fakebin.Install(t, dir, "copilot", "lister", map[string]string{"stdout": "  `model`: x\n    * \"gpt-6\"\n", "version": "copilot 1"})
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
		"copilot,copilot 1,\"\",\"no list: copilot help config lists no models under `model`; its format may have changed\"",
		"agy,\"\",\"\",\"no list: agy is not signed in; run agy and sign in\"")
	if strings.Contains(out+errOut, invented) {
		t.Fatalf("output holds the secret: %q %q", out, errOut)
	}
}

func TestModelsReportsAMissingCopilot(t *testing.T) {
	h := newHarness(t)
	dir := listers(t, h, "")
	if err := os.Remove(exe(dir, "copilot")); err != nil {
		t.Fatal(err)
	}
	has(t, "models copilot", h.ok("models", "--refresh", "copilot"), "copilot,\"\",\"\",copilot not found on PATH")
}

func TestModelsNeverPrintOrCacheASecret(t *testing.T) {
	h := newHarness(t)
	listers(t, h, "")
	h.ok("init")
	out := h.ok("models", "--refresh")
	for _, text := range []string{out, h.ok("models")} {
		if strings.Contains(text, invented) {
			t.Fatalf("output holds the secret: %q", text)
		}
	}
	files, err := filepath.Glob(filepath.Join(h.vars["SECONDHAND_HOME"], "models", "*"))
	if err != nil || len(files) != 12 {
		t.Fatalf("cache files = %v, %v", files, err)
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err != nil || strings.Contains(string(b), invented) || strings.Contains(string(b), "INVENTED") {
			t.Fatalf("%s holds the secret: %q, %v", f, b, err)
		}
	}
}

func TestAttemptStartMarksAModelThatLeftTheList(t *testing.T) {
	fx := newAttemptFixture(t)
	dir := listers(t, fx.h, "")
	fx.h.ok("models", "--refresh")
	fakebin.Install(t, dir, "codex", "lister", map[string]string{"stdout": codexLater, "version": "codex 9.9"})
	fakebin.Install(t, dir, "claude", "lister", map[string]string{"stdout": strings.ReplaceAll(claudeAnswer, `,{"value":"acme-opus","displayName":"Acme","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]}`, ""), "version": "claude 9.9"})
	fx.h.ok("models", "--refresh", "codex")
	fx.h.ok("models", "--refresh", "claude")
	has(t, "codex start", fx.h.ok("attempt", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low", "--prompt-file", fx.brief, "t1"), "model gpt-6-luna is no longer listed by codex")
	fx.h.ok("attempt", "stop", "a1")
	has(t, "claude start", fx.h.ok("attempt", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "low", "--prompt-file", fx.brief, "t1"), "model acme-opus is no longer listed by claude; starting anyway")
}

func TestNoRequestPathRunsAHarness(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	log := filepath.Join(t.TempDir(), "log")
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		fakebin.Install(t, h.vars["PATH"], name, "lister", map[string]string{"log": log, "stdout": "gemini-3.8-flash-low\tG\n", "hold": "300s"})
	}
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(`{}`), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
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
