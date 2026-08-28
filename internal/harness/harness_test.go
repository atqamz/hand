package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/state"
)

func buildSpec(t *testing.T, name string, options Options) launch.LaunchSpec {
	t.Helper()
	options = withTestReportPath(options)
	spec, err := Build(name, options)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func withTestReportPath(options Options) Options {
	if options.ReportPath == "" {
		options.ReportPath = "/tmp/state/task-1.status"
	}
	return options
}

func testBriefPrompt(t *testing.T, options Options) string {
	t.Helper()
	prompt, err := briefPrompt(withTestReportPath(options))
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

func TestBuildUnrecognizedHarness(t *testing.T) {
	if _, err := Build("nonexistent", Options{}); err == nil {
		t.Fatal("expected error for unrecognized harness")
	}
}

func TestBuildRejectsMissingReportPath(t *testing.T) {
	_, err := Build(Claude, Options{Brief: "/tmp/brief.md"})
	if err == nil || !strings.Contains(err.Error(), "report path") {
		t.Fatalf("Build() error = %v, want missing report path error", err)
	}
}

func TestIsSupported(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode, Antigravity} {
		if !IsSupported(name) {
			t.Errorf("IsSupported(%q) = false, want true", name)
		}
	}
	if IsSupported("nonexistent") {
		t.Error("IsSupported(nonexistent) = true, want false")
	}
}

func TestBuildAntigravity(t *testing.T) {
	options := Options{Worktree: "/tmp/space unicode/wt", Brief: "/tmp/space unicode/wt/brief.md"}
	spec := buildSpec(t, Antigravity, options)
	want := launch.LaunchSpec{
		Executable: "agy",
		Args:       []string{"--dangerously-skip-permissions", "--output-format", "stream-json", "--print-timeout", antigravityPrintTimeout, "-p", testBriefPrompt(t, options)},
		Cwd:        options.Worktree,
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
	if strings.Contains(spec.Executable, " ") || strings.Contains(spec.Executable, "cd ") {
		t.Fatalf("executable %q contains shell syntax", spec.Executable)
	}
	for _, value := range spec.Args {
		if strings.HasPrefix(value, "cd ") || strings.HasPrefix(value, "HAND_ROLE=") || strings.HasPrefix(value, "HAND_HOME=") {
			t.Fatalf("argument %q contains shell launch syntax", value)
		}
	}
	if contains(spec.Args, "--input-format") {
		t.Fatalf("args = %#v, one-shot -p must not opt into stdin streaming", spec.Args)
	}
}

func TestBuildAntigravityWithModelAndEffort(t *testing.T) {
	options := Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "gemini-3.5-flash-medium", Effort: "high"}
	spec := buildSpec(t, Antigravity, options)
	want := []string{"--dangerously-skip-permissions", "--output-format", "stream-json", "--print-timeout", antigravityPrintTimeout, "--model", options.Model, "--effort", options.Effort, "-p", testBriefPrompt(t, options)}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args = %#v, want %#v", spec.Args, want)
	}
}

func TestBuildAntigravityRejectsUnsupportedEffort(t *testing.T) {
	_, err := Build(Antigravity, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Effort: "x-high"})
	if err == nil || !strings.Contains(err.Error(), "low, medium, or high") {
		t.Fatalf("Build() error = %v, want unsupported effort error", err)
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
	wantPrompt := testBriefPrompt(t, Options{Brief: "/tmp/data/fix-login/brief.md"})
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
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "disable_paste_burst=true", testBriefPrompt(t, Options{Brief: "/tmp/brief.md"})}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args = %#v, want %#v", spec.Args, want)
	}
}

func TestBuildCodexWithModelAndEffort(t *testing.T) {
	spec := buildSpec(t, Codex, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "gpt-5.6-codex", Effort: "high"})
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "disable_paste_burst=true", "--model", "gpt-5.6-codex", "-c", `model_reasoning_effort="high"`, testBriefPrompt(t, Options{Brief: "/tmp/brief.md"})}
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

// Build is a pure function from Options to a LaunchSpec: the append that carries the report
// path and operator-decision rule to grok and pi is AppendPromptToBrief's job, called
// separately by the provisioning path, never by Build itself (atqamz/hand#418).
func TestBuildNeverTouchesTheBriefFile(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
		briefPath := writeTestBrief(t, "do the task.\n")
		if _, err := Build(name, Options{Worktree: "/tmp/wt", Brief: briefPath, ReportPath: "/tmp/state/task-1.status"}); err != nil {
			t.Fatalf("Build(%q) = %v", name, err)
		}
		data, err := os.ReadFile(briefPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "do the task.\n" {
			t.Fatalf("Build(%q) modified the brief file, got %q", name, data)
		}
	}
}

func TestAppendPromptToBriefGrokAndPi(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
		briefPath := writeTestBrief(t, "do the task.\n")
		options := Options{Brief: briefPath, ReportPath: "/tmp/state/task-1.status"}
		if err := AppendPromptToBrief(name, options); err != nil {
			t.Fatalf("AppendPromptToBrief(%q) = %v", name, err)
		}
		assertBriefCarriesLaunchStatement(t, briefPath, options)
	}
}

// atqamz/hand#448: promote compares brief.Digest from before and after this append runs, so the
// two can never disagree about what "unchanged" means. Produces the appendix through the real
// path rather than a hand-written literal, so a format drift here would fail this test directly.
func TestAppendPromptToBriefLeavesBriefDigestUnchanged(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
		t.Run(name, func(t *testing.T) {
			briefPath := writeTestBrief(t, "implement the fix.\n")
			before, err := brief.Digest(briefPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := AppendPromptToBrief(name, Options{Brief: briefPath, ReportPath: "/tmp/state/task-1.status"}); err != nil {
				t.Fatal(err)
			}
			after, err := brief.Digest(briefPath)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("Digest after AppendPromptToBrief(%q) = %q, want %q unchanged", name, after, before)
			}
		})
	}
}

func TestAppendPromptToBriefIsIdempotent(t *testing.T) {
	briefPath := writeTestBrief(t, "do the task.\n")
	options := Options{Brief: briefPath, ReportPath: "/tmp/state/task-1.status"}
	if err := AppendPromptToBrief(Grok, options); err != nil {
		t.Fatal(err)
	}
	once, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendPromptToBrief(Grok, options); err != nil {
		t.Fatal(err)
	}
	twice, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatalf("AppendPromptToBrief grew a second copy on the second call:\nfirst:  %q\nsecond: %q", once, twice)
	}
}

func TestAppendPromptToBriefNoopForPromptCapableHarnesses(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		briefPath := writeTestBrief(t, "do the task.\n")
		if err := AppendPromptToBrief(name, Options{Brief: briefPath, ReportPath: "/tmp/state/task-1.status"}); err != nil {
			t.Fatalf("AppendPromptToBrief(%q) = %v, want nil (no-op)", name, err)
		}
		data, err := os.ReadFile(briefPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "do the task.\n" {
			t.Fatalf("AppendPromptToBrief(%q) touched the brief file, got %q", name, data)
		}
	}
	// A no-op harness never resolves ReportPath, so a nonexistent brief path is not an error either.
	if err := AppendPromptToBrief(Claude, Options{Brief: "/does/not/exist.md"}); err != nil {
		t.Fatalf("AppendPromptToBrief(claude) = %v, want nil", err)
	}
}

func TestAppendPromptToBriefRejectsMissingReportPath(t *testing.T) {
	for _, name := range []string{Grok, Pi} {
		briefPath := writeTestBrief(t, "do the task.\n")
		err := AppendPromptToBrief(name, Options{Brief: briefPath})
		if err == nil || !strings.Contains(err.Error(), "report path") {
			t.Fatalf("AppendPromptToBrief(%q) error = %v, want missing report path error", name, err)
		}
		if data, readErr := os.ReadFile(briefPath); readErr != nil || strings.Contains(string(data), brief.AppendMarker) {
			t.Fatalf("AppendPromptToBrief(%q) refusal must not touch the brief file, got %q", name, data)
		}
	}
}

// Gives each caller its own file under t.TempDir() rather than a fixed /tmp path, since
// AppendPromptToBrief mutates it and a shared path would leak state between tests.
func writeTestBrief(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertBriefCarriesLaunchStatement(t *testing.T, briefPath string, options Options) {
	t.Helper()
	data, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "do the task.") {
		t.Fatalf("brief content = %q, want the supervisor's original brief preserved at the top", content)
	}
	for _, want := range []string{
		brief.AppendMarker,
		options.ReportPath,
		"working:", "done:", "failed:", "blocked:", "needs-decision:", "paused:",
		"plain shell redirection",
		agentsmd.OperatorDecisionRule,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("brief content = %q, want launch statement to contain %q", content, want)
		}
	}
}

func TestBuildOpenCode(t *testing.T) {
	spec := buildSpec(t, OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	want := launch.LaunchSpec{
		Executable: OpenCode,
		Args:       []string{"--prompt", testBriefPrompt(t, Options{Brief: "/tmp/brief.md"})},
		Env:        map[string]string{"OPENCODE_CONFIG_CONTENT": `{"permission":{"*":"allow"}}`},
		Cwd:        "/tmp/wt",
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v, want %+v", spec, want)
	}
}

func TestBuildFrontMatterDisclaimer(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", BriefHasFrontMatter: true})
		if !containsText(spec.Args, "dispatch metadata") {
			t.Fatalf("Build(%q) args = %#v, want front matter disclaimer", name, spec.Args)
		}
	}
}

func TestBuildPromptDeclaresReportChannel(t *testing.T) {
	options := Options{
		Brief:      "/tmp/data/task-1/brief.md",
		ReportPath: "/tmp/state/task-1.status",
	}
	spec := buildSpec(t, Claude, options)
	for _, want := range []string{
		options.ReportPath,
		"working:",
		"done:",
		"failed:",
		"blocked:",
		"needs-decision:",
		"paused:",
		"plain shell redirection",
	} {
		if !containsText(spec.Args, want) {
			t.Fatalf("Build prompt = %#v, want report-channel guidance %q", spec.Args, want)
		}
	}
}

func TestBuildMechanicalExecutionGuidance(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
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

func TestBuildShipCarriesDeliveryAuthorization(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Kind: state.KindShip})
		for _, want := range []string{"authorized to commit, push your branch, and open the pull request", "merging and closing the issue are the supervisor's action only"} {
			if !containsText(spec.Args, want) {
				t.Fatalf("Build(%q) args = %#v, want ship delivery authorization %q", name, spec.Args, want)
			}
		}
		if containsText(spec.Args, "must not commit, push, or open a pull request") {
			t.Fatalf("Build(%q) args = %#v, ship must not carry the scout refusal", name, spec.Args)
		}
	}
}

func TestBuildScoutCarriesNoDeliveryGrant(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Kind: state.KindScout})
		if !containsText(spec.Args, "must not commit, push, or open a pull request") {
			t.Fatalf("Build(%q) args = %#v, want scout refusal", name, spec.Args)
		}
		if containsText(spec.Args, "authorized to commit, push your branch") {
			t.Fatalf("Build(%q) args = %#v, scout must not carry the ship grant", name, spec.Args)
		}
	}
}

func TestBuildWithoutKindCarriesNoAuthorizationStatement(t *testing.T) {
	spec := buildSpec(t, Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if containsText(spec.Args, "authorized to commit, push your branch") || containsText(spec.Args, "must not commit, push, or open a pull request") {
		t.Fatalf("Build args = %#v, want no authorization statement without a kind", spec.Args)
	}
}

func TestBuildCarriesOperatorDecisionRule(t *testing.T) {
	for _, name := range []string{Claude, Codex, OpenCode, Antigravity} {
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
		if !containsText(spec.Args, agentsmd.OperatorDecisionRule) {
			t.Errorf("Build(%q) args = %#v, want operator-decision rule", name, spec.Args)
		}
	}
}

func TestSupportsModel(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode, Antigravity} {
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
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode, Antigravity} {
		effort := "some-effort"
		if name == Antigravity {
			effort = "high"
		}
		spec := buildSpec(t, name, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Effort: effort})
		emits := hasPair(spec.Args, "--effort", effort) || contains(spec.Args, fmt.Sprintf(`model_reasoning_effort="%s"`, effort))
		if emits != SupportsEffort(name) {
			t.Errorf("SupportsEffort(%q) = %v but args = %#v", name, SupportsEffort(name), spec.Args)
		}
	}
	if SupportsEffort("nonexistent") {
		t.Error("SupportsEffort(nonexistent) = true, want false")
	}
}

func TestCarriesPrompt(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode, Antigravity} {
		briefPath := writeTestBrief(t, "do the task.\n")
		options := withTestReportPath(Options{Worktree: "/tmp/wt", Brief: briefPath})
		// Mirrors the provisioning path: AppendPromptToBrief runs before Build, and is a no-op
		// for a harness that carries the prompt as a CLI argument instead.
		if err := AppendPromptToBrief(name, options); err != nil {
			t.Fatal(err)
		}
		spec := buildSpec(t, name, options)
		carried := containsText(spec.Args, agentsmd.OperatorDecisionRule)
		if !carried {
			data, err := os.ReadFile(briefPath)
			if err != nil {
				t.Fatal(err)
			}
			carried = strings.Contains(string(data), agentsmd.OperatorDecisionRule)
		}
		if carried != CarriesPrompt(name) {
			t.Errorf("CarriesPrompt(%q) = %v but delivered = %v (args = %#v)", name, CarriesPrompt(name), carried, spec.Args)
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
	for _, name := range []string{Grok, Pi, Antigravity, "nonexistent"} {
		if AgentDetectionVerified(name) {
			t.Errorf("AgentDetectionVerified(%q) = true, want false", name)
		}
	}
}

func TestFirstRunPromptsWithoutVerifiedSignatures(t *testing.T) {
	for _, name := range []string{Grok, Pi, OpenCode, Antigravity, "nonexistent"} {
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
