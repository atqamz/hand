package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func TestSupervisorStartWarnsAboutAnUnverifiedModelAndLaunchesIt(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		warn string
		flag string
	}{
		{"claude gateway name", []string{"--harness", "claude", "--model", "acme-opus", "--effort", "high"}, `warn: "claude model acme-opus is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list; starting anyway"`, "acme-opus"},
		{"claude bedrock id", []string{"--harness", "claude", "--model", "us.anthropic.claude-opus-5-5-v1:0", "--effort", "high"}, "warn:", "us.anthropic.claude-opus-5-5-v1:0"},
		{"codex without a cache", []string{"--harness", "codex", "--model", "acme-coder-1", "--effort", "high"}, `warn: "codex model acme-coder-1 could not be checked`, "acme-coder-1"},
		{"agy unlisted", []string{"--harness", "agy", "--model", "gemini-9"}, "warn: agy model gemini-9 is not in `agy models`", "gemini-9"},
		{"agy without a list", []string{"--harness", "agy", "--model", "gemini-9"}, "warn: \"agy model gemini-9 could not be checked: no model list yet; run `hand models --refresh agy`", "gemini-9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, rt := newSupervisorFixture(t)
			if c.name == "agy unlisted" {
				h.ok("models", "--refresh", "agy")
			}
			out := h.ok(append([]string{"supervisor", "start"}, c.args...)...)
			has(t, "start", out, c.warn, "status: running")
			if !slices.Contains(rt.lastCreate().Command, c.flag) {
				t.Fatalf("argv = %q", rt.lastCreate().Command)
			}
		})
	}
}

func TestSupervisorStartPrintsNoWarnForAVerifiedModel(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	if out := startClaudeSupervisor(h); strings.Contains(out, "warn") {
		t.Fatalf("start = %q", out)
	}
}

func TestSupervisorStartRefusesWhatIsMalformed(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--harness", "claude", "--model", "my model", "--effort", "high"}, `claude model "my model"`},
		{[]string{"--harness", "claude", "--model", "-x", "--effort", "high"}, `claude model "-x"`},
		{[]string{"--harness", "claude", "--model", "opus"}, "claude effort"},
		{[]string{"--harness", "codex", "--model", "gpt-6-luna", "--effort", "--max"}, "codex effort"},
		{[]string{"--harness", "agy", "--model", "gemini-3.8-flash-low", "--effort", "high"}, "agy names the effort"},
		{[]string{"--harness", "opencode", "--model", "acme/acme-coder-1#deep"}, "provider/id"},
		{[]string{"--harness", "opencode", "--model", "acme-coder-1"}, "provider/id"},
		{[]string{"--harness", "opencode", "--effort", "deep"}, "variant of a model"},
		{[]string{"--harness", "gemini", "--model", "x", "--effort", "low"}, `harness "gemini"`},
	} {
		h, rt := newSupervisorFixture(t)
		_, errOut, code := h.run(append([]string{"supervisor", "start"}, c.args...)...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%q: code=%d stderr=%q", c.args, code, errOut)
		}
		rt.mu.Lock()
		made := len(rt.creates)
		rt.mu.Unlock()
		if made != 0 {
			t.Fatalf("%q launched %d terminals", c.args, made)
		}
	}
}

func TestSupervisorLaunchFailureNamesTheModel(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	rt.srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "create_failed", Message: "terminal failed to start"}
	})
	_, errOut, code := h.run("supervisor", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high")
	if code == 0 || !strings.Contains(errOut, "terminal failed to start") || !strings.Contains(errOut, "launching claude acme-opus") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: claude acme-opus high", "status: failed", "launching claude acme-opus")
}

func TestSupervisorResumeAndSwitchRepeatTheWarning(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	h.ok("supervisor", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high")
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "warn:", "acme-opus", "supervisor: s2")
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--model", "my-gateway/claude-opus-5-5"), "switch: applied", `warn: "claude model my-gateway/claude-opus-5-5`)
	if _, errOut, code := h.run("supervisor", "switch", "--model", "a b"); code != 2 || !strings.Contains(errOut, `claude model "a b"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptStartWarnsAboutAnUnverifiedModel(t *testing.T) {
	fx := newAttemptFixture(t)
	out := fx.h.ok("attempt", "start", "--harness", "claude", "--model", "acme-opus", "--effort", "high", "--prompt-file", fx.brief, "t1")
	has(t, "start", out, "attempt: a1", `warn: "claude model acme-opus`)
	if !slices.Contains(fx.rt.lastCreate().Command, "acme-opus") {
		t.Fatalf("argv = %q", fx.rt.lastCreate().Command)
	}
}

func TestAttemptStartWarnsWithoutACodexCache(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.vars["HOME"] = t.TempDir()
	out := fx.h.ok("attempt", "start", "--harness", "codex", "--model", "acme-coder-1", "--effort", "high", "--prompt-file", fx.brief, "t1")
	has(t, "start", out, `warn: "codex model acme-coder-1 could not be checked`)
}

func TestRouteListKeepsValidYesAndAddsTheWarning(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	policy := `{"profiles":{"alias":{"harness":"claude","model":"sonnet","effort":"high"},"gateway":{"harness":"claude","model":"acme-opus","effort":"high"},"bedrock":{"harness":"claude","model":"us.anthropic.claude-opus-5-5-v1:0","effort":"high"},"codex-custom":{"harness":"codex","model":"acme-coder-1","effort":"high"},"agy-x":{"harness":"agy","model":"gemini-9"},"cp-auto":{"harness":"copilot","model":"auto"},"cp-listed":{"harness":"copilot","model":"gpt-6-luna","effort":"high"},"cp-unlisted":{"harness":"copilot","model":"acme-coder-1"},"cp-auto-effort":{"harness":"copilot","model":"auto","effort":"high"},"oc-plain":{"harness":"opencode"},"oc-model":{"harness":"opencode","model":"acme/acme-coder-1"},"oc-deep":{"harness":"opencode","model":"local/m2","effort":"deep"},"oc-hash":{"harness":"opencode","model":"local/m2#deep"},"spaced":{"harness":"claude","model":"my model","effort":"high"}}}`
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	list := h.ok("route", "list")
	has(t, "route list", list,
		`alias,claude,sonnet,high,yes,""`,
		`gateway,claude,acme-opus,high,yes,"claude model acme-opus is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list; starting anyway"`,
		`bedrock,claude,"us.anthropic.claude-opus-5-5-v1:0",high,yes,"claude model us.anthropic.claude-opus-5-5-v1:0 is not an alias`,
		`codex-custom,codex,acme-coder-1,high,yes,"codex model acme-coder-1 could not be checked: codex debug models --bundled printed output it does not recognise, and models_cache.json is unusable; starting anyway"`,
		"agy-x,agy,gemini-9,\"\",yes,agy model gemini-9 is not in `agy models`; starting anyway",
		`cp-auto,copilot,auto,"",yes,""`,
		`cp-listed,copilot,gpt-6-luna,high,yes,""`,
		"cp-unlisted,copilot,acme-coder-1,\"\",yes,copilot model acme-coder-1 is not in `copilot help config`; starting anyway",
		`cp-auto-effort,copilot,auto,high,yes,copilot model auto takes no effort; starting anyway`,
		"copilot,gpt-6-luna,none minimal low medium high xhigh max",
		`oc-plain,opencode,"","",yes,""`,
		`oc-model,opencode,acme/acme-coder-1,"",yes,"opencode model acme/acme-coder-1 could not be checked: `,
		`oc-deep,opencode,local/m2,deep,yes,"opencode model local/m2 could not be checked: `,
		`oc-hash,opencode,local/m2#deep,"","invalid: opencode model \"local/m2#deep\" must be provider/id`,
		`spaced,claude,my model,high,"invalid: claude model \"my model\" must be`,
	)
}
