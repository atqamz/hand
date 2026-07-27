package harness

import (
	"strings"
	"testing"
)

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

func TestBuildAlwaysCdsIntoWorktree(t *testing.T) {
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		got, err := Build(name, Options{Worktree: "/tmp/wt", Brief: "/tmp/wt/brief.md"})
		if err != nil {
			t.Fatalf("Build(%q) error: %v", name, err)
		}
		if !strings.HasPrefix(got, "cd '/tmp/wt' && ") {
			t.Errorf("Build(%q) = %q, want cd prefix", name, got)
		}
	}
}

func TestBuildClaude(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/data/fix-login/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --dangerously-skip-permissions 'Read the brief at /tmp/data/fix-login/brief.md and carry out the task it describes.'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildClaudeWithModelAndEffort(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "sonnet", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --dangerously-skip-permissions --model 'sonnet' --effort 'low' 'Read the brief at /tmp/brief.md and carry out the task it describes.'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestBuildClaudeNeverHeadless guards against a silent regression to --print,
// which would strand hand send and hand watch with no running pane to steer.
func TestBuildClaudeNeverHeadless(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "--print") {
		t.Fatalf("got %q, want no --print (headless) flag", got)
	}
	if !strings.Contains(got, "--dangerously-skip-permissions") {
		t.Fatalf("got %q, want --dangerously-skip-permissions so an unattended worker never stalls on a permission prompt", got)
	}
}

func TestBuildCodex(t *testing.T) {
	got, err := Build(Codex, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && codex --file '/tmp/brief.md'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildGrok(t *testing.T) {
	got, err := Build(Grok, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && grok --trust --file '/tmp/brief.md'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildPi(t *testing.T) {
	got, err := Build(Pi, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && pi '/tmp/brief.md'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildOpenCode(t *testing.T) {
	got, err := Build(OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd '/tmp/wt' && OPENCODE_CONFIG_CONTENT='{\"permission\":{\"*\":\"allow\"}}' opencode --prompt 'Read the brief at /tmp/brief.md and carry out the task it describes.'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildClaudeFrontMatterDisclaimer(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", BriefHasFrontMatter: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "dispatch metadata") {
		t.Fatalf("got %q, want the front matter disclaimed", got)
	}
}

func TestBuildClaudeNoFrontMatterUnchanged(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "dispatch metadata") {
		t.Fatalf("got %q, want no disclaimer for a brief with no front matter", got)
	}
}

func TestBuildOpenCodeWithModel(t *testing.T) {
	got, err := Build(OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "--model 'opus'") {
		t.Fatalf("got %q, want --model flag", got)
	}
}

// TestBuildOpenCodeNeverHeadless guards against a silent regression to
// `opencode run`, which exits after one reply and leaves no pane to steer.
func TestBuildOpenCodeNeverHeadless(t *testing.T) {
	got, err := Build(OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "opencode run") {
		t.Fatalf("got %q, want no headless \"opencode run\" invocation", got)
	}
	if !strings.Contains(got, `OPENCODE_CONFIG_CONTENT`) {
		t.Fatalf("got %q, want OPENCODE_CONFIG_CONTENT so an unattended worker never stalls on a permission prompt", got)
	}
}

func TestBuildOpenCodeFrontMatterDisclaimer(t *testing.T) {
	got, err := Build(OpenCode, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", BriefHasFrontMatter: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "dispatch metadata") {
		t.Fatalf("got %q, want the front matter disclaimed", got)
	}
}

func TestSupportsEffort(t *testing.T) {
	if !SupportsEffort(Claude) {
		t.Error("SupportsEffort(claude) = false, want true")
	}
	for _, name := range []string{Codex, Grok, Pi, OpenCode, "nonexistent"} {
		if SupportsEffort(name) {
			t.Errorf("SupportsEffort(%q) = true, want false", name)
		}
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got, err := Build(Pi, Options{Worktree: "/tmp/wt", Brief: "/tmp/it's/brief.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := `cd '/tmp/wt' && pi '/tmp/it'\''s/brief.md'`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestFirstRunPromptsClaude pins the shape confirmLaunch depends on: a startup signature for
// each frame claude settles into, answerable dialogs carrying keys, and the managed-settings
// dialog catalogued as recognized-but-refused so it fails fast instead of looking uncatalogued.
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
	if prompts.Ready.MatchString("cd '/tmp/wt' && claude --dangerously-skip-permissions 'Read the brief'") {
		t.Fatal("readiness signature matches the echoed launch command, so a pane that never started reads as ready")
	}

	byName := map[string]FirstRunPrompt{}
	for _, prompt := range prompts.Known {
		if (len(prompt.Keys) == 0) == (prompt.Refuse == "") {
			t.Fatalf("prompt %q must set exactly one of Keys and Refuse, got %+v", prompt.Name, prompt)
		}
		byName[prompt.Name] = prompt
	}

	bypass := byName["bypass permissions"]
	if !bypass.Match.MatchString("WARNING: Bypass Permissions mode") {
		t.Fatal("bypass permissions signature does not match the dialog")
	}
	if strings.Join(bypass.Keys, ",") != "Down,Enter" {
		t.Fatalf("bypass permissions keys = %v, want Down before Enter (a bare Enter lands on \"No, exit\" and quits claude)", bypass.Keys)
	}
	if got := byName["workspace trust"]; strings.Join(got.Keys, ",") != "Enter" {
		t.Fatalf("workspace trust keys = %v, want Enter", got.Keys)
	}
	managed := byName["managed settings"]
	if managed.Refuse == "" {
		t.Fatal("managed settings must be refused, not answered on the operator's behalf")
	}
	if !managed.Match.MatchString("Managed settings require approval") || !managed.Match.MatchString("Yes, I trust these settings") {
		t.Fatal("managed settings signature does not match the dialog")
	}
}

// TestAgentDetectionVerified pins the two harnesses actually run in a real pane and observed
// being labeled by herdr; the rest must stay false until each is exercised the same way.
func TestAgentDetectionVerified(t *testing.T) {
	for _, name := range []string{Claude, OpenCode} {
		if !AgentDetectionVerified(name) {
			t.Errorf("AgentDetectionVerified(%q) = false, want true", name)
		}
	}
	for _, name := range []string{Codex, Grok, Pi, "nonexistent"} {
		if AgentDetectionVerified(name) {
			t.Errorf("AgentDetectionVerified(%q) = true, want false until herdr detection is exercised against it", name)
		}
	}
}

func TestFirstRunPromptsUnverifiedHarness(t *testing.T) {
	for _, name := range []string{Codex, Grok, Pi, OpenCode, "nonexistent"} {
		if got := FirstRunPromptsFor(name); got.Ready != nil || got.Known != nil || got.Unrecognized != nil {
			t.Errorf("FirstRunPromptsFor(%q) = %+v, want no unverified signatures", name, got)
		}
	}
}
