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
