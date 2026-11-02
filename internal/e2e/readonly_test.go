package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
	"github.com/atqamz/hand/internal/state"
)

func TestClaudeReadonly(t *testing.T) {
	k := claudeKit
	if runtime.GOOS == "windows" {
		t.Skip("read-only access is refused on Windows")
	}
	sb := newSandbox(t)
	repo := sb.repo("app")
	hookMarker := filepath.Join(sb.root, "hook-ran")
	mcpMarker := filepath.Join(sb.root, "mcp-ran")
	userMcpMarker := filepath.Join(sb.root, "user-mcp-ran")
	diffOut := filepath.Join(repo, "diff-output.txt")
	sb.write(filepath.Join(repo, ".claude", "settings.json"), mustJSON(map[string]any{
		"permissions": map[string]any{"allow": []string{"Bash(touch *)", "Bash(git *)"}},
		"hooks":       map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "touch '" + hookMarker + "'"}}}}},
	}))
	sb.write(filepath.Join(repo, ".mcp.json"), mustJSON(map[string]any{"mcpServers": map[string]any{"writer": map[string]any{"command": "touch", "args": []string{mcpMarker}}}}))
	sb.git(repo, "add", ".claude", ".mcp.json")
	sb.git(repo, "commit", "-q", "-m", "project settings")
	sb.startMock(
		mockllm.Step{Shell: k.shell("git", "tag", "written-by-a-readonly-worker")},
		mockllm.Step{Shell: "touch written-by-a-readonly-worker.txt"},
		mockllm.Step{Shell: "git diff --output='" + diffOut + "' HEAD~1"},
		mockllm.Step{Shell: "'" + handBin + "' report add --status done --file - <<'END_OF_REPORT'\n" + reportBody + "\nEND_OF_REPORT"},
		mockllm.Step{Text: "Reported."},
	)
	k.seed(sb, sb.mock.URL)
	userState := filepath.Join(sb.home, ".claude.json")
	var user map[string]any
	if raw, err := os.ReadFile(userState); err != nil || json.Unmarshal(raw, &user) != nil {
		t.Fatalf("read %s: %v", userState, err)
	}
	user["mcpServers"] = map[string]any{"own": map[string]any{"command": "touch", "args": []string{userMcpMarker}}}
	sb.write(userState, mustJSON(user))
	sb.init()
	sb.write(filepath.Join(sb.fleet, "routing.json"), fmt.Sprintf(`{"profiles":{"look":{"harness":%q,"model":%q,"effort":%q,"access":"readonly"}}}`, k.name, k.model, k.effort))
	sb.ok("project", "add", "app", repo)
	sb.ok("task", "add", "--goal", "Look", "app", "Look")
	sb.ok("task", "start", "t1")
	briefFile := filepath.Join(sb.root, "brief.md")
	sb.write(briefFile, "Review the repository and change nothing.")

	sb.ok("attempt", "start", "--profile", "look", "--prompt-file", briefFile, "t1")
	if got := field(sb.ok("attempt", "show", "a1"), "access"); got != state.AccessReadonly {
		t.Fatalf("attempt access = %q, want %q", got, state.AccessReadonly)
	}
	sb.until("the worker's report", 90*time.Second, func() bool { return len(sb.reports()) > 0 })
	if r := sb.reports()[0]; r.Status != state.ReportDone || strings.TrimSpace(r.Body) != reportBody {
		t.Fatalf("report = %+v, want the scripted done report", r)
	}
	first, ok := sb.turn(0)
	if !ok {
		t.Fatal("the model got no first request")
	}
	var offered struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(first.Body, &offered); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range offered.Tools {
		if tool.Name != "DeferredToolPlaceholder" {
			names = append(names, tool.Name)
		}
	}
	slices.Sort(names)
	if want := []string{"Bash", "Glob", "Grep", "Read"}; !slices.Equal(names, want) {
		t.Fatalf("tools offered to the read-only worker = %q, want %q", names, want)
	}
	if tags := strings.TrimSpace(sb.git(repo, "tag", "--list")); tags != "" {
		t.Errorf("the read-only worker wrote the repository: tags %q", tags)
	}
	var worktree string
	for _, line := range strings.Split(sb.git(repo, "worktree", "list", "--porcelain"), "\n") {
		if dir, ok := strings.CutPrefix(line, "worktree "); ok && !sameDir(dir, repo) {
			worktree = dir
		}
	}
	if worktree == "" {
		t.Fatal("the attempt has no worktree")
	}
	for _, path := range []string{filepath.Join(worktree, "written-by-a-readonly-worker.txt"), diffOut, hookMarker, mcpMarker, userMcpMarker} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("the read-only worker or the repository's own settings wrote a file: %s", path)
		}
	}
}
