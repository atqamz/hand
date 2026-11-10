package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
)

func TestClaudeMCP(t *testing.T) {
	k := claudeKit
	if runtime.GOOS == "windows" {
		t.Skip("the scripted MCP servers run touch")
	}
	sb := newSandbox(t)
	repo := sb.repo("app")
	userMarker := filepath.Join(sb.root, "user-mcp-ran")
	fileMarker := filepath.Join(sb.root, "file-mcp-ran")
	projectMarker := filepath.Join(sb.root, "project-mcp-ran")
	sb.write(filepath.Join(repo, ".mcp.json"), mustJSON(map[string]any{"mcpServers": map[string]any{"project": map[string]any{"command": "touch", "args": []string{projectMarker}}}}))
	sb.git(repo, "add", ".mcp.json")
	sb.git(repo, "commit", "-q", "-m", "project mcp")
	report := mockllm.Step{Shell: "'" + handBin + "' report add --status done --file - <<'END_OF_REPORT'\n" + reportBody + "\nEND_OF_REPORT"}
	sb.startMock(report, mockllm.Step{Text: "Reported."})
	k.seed(sb, sb.mock.URL)
	userState := filepath.Join(sb.home, ".claude.json")
	var user map[string]any
	if raw, err := os.ReadFile(userState); err != nil || json.Unmarshal(raw, &user) != nil {
		t.Fatalf("read %s: %v", userState, err)
	}
	user["mcpServers"] = map[string]any{"own": map[string]any{"command": "touch", "args": []string{userMarker}}}
	sb.write(userState, mustJSON(user))
	sb.init()
	sb.write(filepath.Join(sb.fleet, "mcp.json"), mustJSON(map[string]any{"mcpServers": map[string]any{"named": map[string]any{"command": "touch", "args": []string{fileMarker}}}}))
	sb.ok("project", "add", "app", repo)
	for _, id := range []string{"t1", "t2"} {
		sb.ok("task", "add", "--goal", "Look", "app", "Look")
		sb.ok("task", "start", id)
	}
	briefFile := filepath.Join(sb.root, "brief.md")
	sb.write(briefFile, "Look around and report.")

	flags := []string{"--harness", k.name, "--model", k.model, "--effort", k.effort, "--prompt-file", briefFile}
	sb.ok(append([]string{"attempt", "start"}, append(flags, "t1")...)...)
	sb.until("the default worker's report", 90*time.Second, func() bool { return len(sb.reports()) > 0 })
	for _, path := range []string{userMarker, fileMarker, projectMarker} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("the default worker started an MCP server it was not given: %s", path)
		}
	}
	out := sb.ok(append([]string{"attempt", "start", "--mcp", "mcp.json"}, append(flags, "t2")...)...)
	wantMCP, err := filepath.EvalSymlinks(filepath.Join(sb.fleet, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if show := sb.ok("attempt", "show", field(out, "attempt")); field(show, "mcp") != wantMCP {
		t.Errorf("attempt show = %q, want the --mcp file", show)
	}
	sb.until("the second worker's report", 90*time.Second, func() bool { return len(sb.reports()) > 1 })
	sb.until("the --mcp file's MCP server", 20*time.Second, func() bool { _, err := os.Stat(fileMarker); return err == nil })
	for _, path := range []string{userMarker, projectMarker} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("the worker with an MCP file also started a server of the user's or the repository's config: %s", path)
		}
	}
}
