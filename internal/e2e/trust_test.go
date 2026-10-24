package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/e2e/mockllm"
)

func TestCodexLeavesAnotherRootsTrustScreenAlone(t *testing.T) {
	sb := newSandbox(t)
	sb.startMock(mockllm.Step{Text: "Never asked."})
	codexKit.seed(sb, sb.mock.URL)
	sb.init()
	main := sb.repo("main")
	linked := filepath.Join(sb.root, "linked")
	sb.git(main, "worktree", "add", "-q", "-b", "side", linked)
	sb.ok("project", "add", "app", linked)
	sb.ok("task", "add", "--goal", "Trust nothing", "app", "Trust")
	sb.ok("task", "start", "t1")
	briefFile := filepath.Join(sb.root, "brief.md")
	sb.write(briefFile, "Say hello.")

	start := sb.ok(append(append([]string{"attempt", "start"}, codexKit.flags()...), "--prompt-file", briefFile, "t1")...)
	if got := field(start, "trust"); got != "not pressed" {
		t.Fatalf("trust = %q, want not pressed\n%s", got, start)
	}
	if field(start, "trusted") != "" {
		t.Fatalf("a trust was saved:\n%s", start)
	}
	screen := sb.ok("attempt", "read", "a1")
	if !strings.Contains(screen, "Trust this folder?") || !strings.Contains(screen, "› 1. Trust and continue") {
		t.Fatalf("the trust screen is gone, so a key reached it:\n%s", screen)
	}
	if !strings.Contains(strings.ReplaceAll(screen, "\n", " "), "repository root:") || !strings.Contains(strings.ToLower(screen), strings.ToLower(main)) {
		t.Fatalf("the screen does not show the other repository root %s:\n%s", main, screen)
	}
	var blocked bool
	for _, e := range sb.events() {
		switch e.Kind {
		case "attempt.keys", "attempt.trusted":
			t.Fatalf("Hand sent keys to the screen of another root: %+v", e)
		case "attempt.blocked":
			blocked = strings.Contains(e.Detail, "not its worktree or project")
		}
	}
	if !blocked {
		t.Fatalf("no attempt.blocked event names the other folder: %+v", sb.events())
	}
	if n := len(sb.mainRequests()); n != 0 {
		t.Fatalf("the model was asked %d times though the trust screen was never answered", n)
	}
	sb.ok("attempt", "stop", "a1")
}
