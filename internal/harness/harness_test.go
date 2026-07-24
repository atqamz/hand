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
	want := "cd '/tmp/wt' && claude --print 'Read the brief at /tmp/data/fix-login/brief.md and carry out the task it describes.'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildClaudeWithModelAndEffort(t *testing.T) {
	got, err := Build(Claude, Options{Worktree: "/tmp/wt", Brief: "/tmp/brief.md", Model: "sonnet", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "--model 'sonnet'") || !strings.Contains(got, "--effort 'low'") {
		t.Fatalf("got %q, want --model and --effort flags", got)
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
	want := "cd '/tmp/wt' && opencode run --file '/tmp/brief.md' 'Follow the attached brief and complete the task.'"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
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
