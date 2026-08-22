package harness

import (
	"reflect"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/launch"
)

func buildSpec(t *testing.T, name string, options Options) launch.LaunchSpec {
	t.Helper()
	spec, err := Build(name, options)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestBuildUnrecognizedHarness(t *testing.T) {
	if _, err := Build("nonexistent", Options{}); err == nil {
		t.Fatal("expected error for unrecognized harness")
	}
}

func TestIsSupported(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		if !IsSupported(name) {
			t.Errorf("IsSupported(%q) = false, want true", name)
		}
	}
	if IsSupported("nonexistent") {
		t.Error("IsSupported(nonexistent) = true, want false")
	}
}

func TestBuildCarriesStructuredCwdAndNoShellPrefixes(t *testing.T) {
	for _, name := range Names() {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/wt/brief.md"})
		if spec.Cwd != "/tmp/wt" {
			t.Errorf("Build(%q).Cwd = %q, want worktree", name, spec.Cwd)
		}
		if strings.Contains(spec.Executable, "cd ") {
			t.Errorf("Build(%q).Executable = %q, contains shell prefix", name, spec.Executable)
		}
		for _, arg := range spec.Args {
			if strings.HasPrefix(arg, "HAND_ROLE=") || strings.HasPrefix(arg, "HAND_HOME=") || strings.HasPrefix(arg, "cd ") {
				t.Errorf("Build(%q) retained shell launch syntax in arg %q", name, arg)
			}
		}
	}
}

func TestBuildClaude(t *testing.T) {
	spec := buildSpec(t, Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/data/fix-login/brief.md"})
	wantPrompt := briefPrompt(Options{Brief: "/tmp/data/fix-login/brief.md"})
	want := launch.LaunchSpec{
		Executable: Claude,
		Args:       []string{"--dangerously-skip-permissions", wantPrompt},
		Env:        map[string]string{"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION": "false"},
		Cwd:        "/tmp/wt",
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
}

func TestBuildClaudeWithModelAndEffort(t *testing.T) {
	spec := buildSpec(t, Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "sonnet", Effort: "low"})
	if !reflect.DeepEqual(spec.Args[:5], []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("args = %#v, want ordered model and effort flags", spec.Args)
	}
}

func TestBuildClaudeNeverHeadless(t *testing.T) {
	spec := buildSpec(t, Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if contains(spec.Args, "--print") {
		t.Fatalf("args = %#v, want no --print flag", spec.Args)
	}
	if !contains(spec.Args, "--dangerously-skip-permissions") {
		t.Fatalf("args = %#v, want unattended permission flag", spec.Args)
	}
}

func TestBuildCodex(t *testing.T) {
	spec := buildSpec(t, Codex, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "disable_paste_burst=true", briefPrompt(Options{Brief: "/tmp/brief.md"})}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args = %#v, want %#v", spec.Args, want)
	}
}

func TestBuildCodexWithModelAndEffort(t *testing.T) {
	spec := buildSpec(t, Codex, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "gpt-5.6-codex", Effort: "high"})
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "disable_paste_burst=true", "--model", "gpt-5.6-codex", "-c", `model_reasoning_effort="high"`, briefPrompt(Options{Brief: "/tmp/brief.md"})}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args = %#v, want %#v", spec.Args, want)
	}
}

func TestBuildCodexOmitsAutoEffort(t *testing.T) {
	spec := buildSpec(t, Codex, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Effort: "auto"})
	if contains(spec.Args, "auto") {
		t.Fatalf("args = %#v, want auto effort omitted", spec.Args)
	}
}

func TestBuildGrok(t *testing.T) {
	spec := buildSpec(t, Grok, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	want := launch.LaunchSpec{Executable: Grok, Args: []string{"--trust", "--file", "/tmp/brief.md"}, Cwd: "/tmp/wt"}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
}

func TestBuildPi(t *testing.T) {
	spec := buildSpec(t, Pi, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	want := launch.LaunchSpec{Executable: Pi, Args: []string{"/tmp/brief.md"}, Cwd: "/tmp/wt"}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
}

func TestBuildOpenCode(t *testing.T) {
	spec := buildSpec(t, OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	want := launch.LaunchSpec{
		Executable: OpenCode,
		Args:       []string{"--prompt", briefPrompt(Options{Brief: "/tmp/brief.md"})},
		Env:        map[string]string{"OPENCODE_CONFIG_CONTENT": `{"permission":{"*":"allow"}}`},
		Cwd:        "/tmp/wt",
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
}

func TestBuildFrontMatterDisclaimer(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", BriefHasFrontMatter: true})
		if !containsText(spec.Args, "dispatch metadata") {
			t.Fatalf("Build(%q) args = %#v, want front matter disclaimer", name, spec.Args)
		}
	}
}

func TestBuildMechanicalExecutionGuidance(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", ExecutionClass: brief.ExecutionClassMechanical})
		for _, want := range []string{"Verify the named files/symbols and plan assumptions before editing.", "stop and report blocked", "Do not redesign the task yourself.", "execute the ordered plan and verification steps"} {
			if !containsText(spec.Args, want) {
				t.Fatalf("Build(%q) args = %#v, want mechanical guidance %q", name, spec.Args, want)
			}
		}
	}
}

func TestBuildStandardAndDeepOmitMechanicalExecutionGuidance(t *testing.T) {
	for _, class := range []brief.ExecutionClass{brief.ExecutionClassStandard, brief.ExecutionClassDeep} {
		spec := buildSpec(t, Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", ExecutionClass: class})
		if containsText(spec.Args, "Do not redesign the task yourself.") || containsText(spec.Args, "Verify the named files/symbols") {
			t.Fatalf("Build(%q) args = %#v, want no mechanical guidance", class, spec.Args)
		}
	}
}

func TestBuildOpenCodeWithModel(t *testing.T) {
	spec := buildSpec(t, OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "opus"})
	if !hasPair(spec.Args, "--model", "opus") {
		t.Fatalf("args = %#v, want --model flag", spec.Args)
	}
}

func TestBuildOpenCodeNeverHeadless(t *testing.T) {
	spec := buildSpec(t, OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if hasPair(spec.Args, "opencode", "run") || contains(spec.Args, "run") {
		t.Fatalf("args = %#v, want interactive opencode invocation", spec.Args)
	}
	if spec.Env["OPENCODE_CONFIG_CONTENT"] == "" {
		t.Fatalf("env = %#v, want OPENCODE_CONFIG_CONTENT", spec.Env)
	}
}

func TestBuildCarriesOperatorDecisionRule(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
		if !containsText(spec.Args, agentsmd.OperatorDecisionRule) {
			t.Errorf("Build(%q) args = %#v, want operator-decision rule", name, spec.Args)
		}
	}
}

func TestSupportsModel(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "some-model"})
		if hasPair(spec.Args, "--model", "some-model") != SupportsModel(name) {
			t.Errorf("SupportsModel(%q) = %v but args = %#v", name, SupportsModel(name), spec.Args)
		}
	}
	if SupportsModel("nonexistent") {
		t.Error("SupportsModel(nonexistent) = true, want false")
	}
}

func TestSupportsEffort(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Effort: "some-effort"})
		emits := hasPair(spec.Args, "--effort", "some-effort") || contains(spec.Args, `model_reasoning_effort="some-effort"`)
		if emits != SupportsEffort(name) {
			t.Errorf("SupportsEffort(%q) = %v but args = %#v", name, SupportsEffort(name), spec.Args)
		}
	}
	if SupportsEffort("nonexistent") {
		t.Error("SupportsEffort(nonexistent) = true, want false")
	}
}

func TestCarriesPrompt(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
		if containsText(spec.Args, agentsmd.OperatorDecisionRule) != CarriesPrompt(name) {
			t.Errorf("CarriesPrompt(%q) = %v but args = %#v", name, CarriesPrompt(name), spec.Args)
		}
	}
	if CarriesPrompt("nonexistent") {
		t.Error("CarriesPrompt(nonexistent) = true, want false")
	}
}

func TestFirstRunPromptsClaude(t *testing.T) {
	prompts := FirstRunPromptsFor(Claude)
	if prompts.Ready == nil || prompts.Unrecognized == nil {
		t.Fatalf("got %+v, want readiness and unrecognized signatures", prompts)
	}
	for _, frame := range []string{"Welcome to Claude Code", "? for shortcuts", "bypass permissions on (shift+tab to cycle)"} {
		if !prompts.Ready.MatchString(frame) {
			t.Errorf("readiness signature does not match claude startup frame %q", frame)
		}
	}
	if prompts.Ready.MatchString("claude --dangerously-skip-permissions Read the brief") {
		t.Fatal("readiness signature matches the echoed launch command")
	}

	byName := map[string]FirstRunPrompt{}
	for _, prompt := range prompts.Known {
		if (len(prompt.Keys) == 0) == (prompt.Refuse == "") {
			t.Fatalf("prompt %q must set exactly one of Keys and Refuse, got %+v", prompt.Name, prompt)
		}
		byName[prompt.Name] = prompt
	}
	if got := byName["bypass permissions"]; strings.Join(got.Keys, ",") != "Down,Enter" {
		t.Fatalf("bypass permissions keys = %v", got.Keys)
	}
	if got := byName["workspace trust"]; strings.Join(got.Keys, ",") != "Enter" {
		t.Fatalf("workspace trust keys = %v", got.Keys)
	}
	managed := byName["managed settings"]
	if managed.Refuse == "" || !managed.Match.MatchString("Managed settings require approval") || !managed.Match.MatchString("Yes, I trust these settings") {
		t.Fatalf("managed settings prompt = %+v", managed)
	}
}

func TestAgentDetectionVerified(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode} {
		if !AgentDetectionVerified(name) {
			t.Errorf("AgentDetectionVerified(%q) = false, want true", name)
		}
	}
	for _, name := range []string{Grok, Pi, "nonexistent"} {
		if AgentDetectionVerified(name) {
			t.Errorf("AgentDetectionVerified(%q) = true, want false", name)
		}
	}
}

func TestFirstRunPromptsWithoutVerifiedSignatures(t *testing.T) {
	for _, name := range []string{Grok, Pi, OpenCode, "nonexistent"} {
		if got := FirstRunPromptsFor(name); got.Ready != nil || got.Known != nil || got.Unrecognized != nil {
			t.Errorf("FirstRunPromptsFor(%q) = %+v, want no unverified signatures", name, got)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsText(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func hasPair(values []string, key, want string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == key && values[i+1] == want {
			return true
		}
	}
	return false
}
