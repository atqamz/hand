package e2e_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
	"github.com/atqamz/hand/internal/state"
)

func TestClaudeReadonly(t *testing.T) {
	k := claudeKit
	if runtime.GOOS == "windows" {
		t.Skip("the read-only report rule is only measured on Linux and macOS")
	}
	sb := newSandbox(t)
	repo := sb.repo("app")
	reportFile := filepath.Join(sb.root, "report.md")
	sb.write(reportFile, reportBody)
	sb.startMock(
		mockllm.Step{Shell: k.shell("git", "tag", "written-by-a-readonly-worker")},
		mockllm.Step{Shell: "'" + handBin + "' report add --status done --file '" + reportFile + "'"},
		mockllm.Step{Text: "Reported."},
	)
	k.seed(sb, sb.mock.URL)
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
	if tags := strings.TrimSpace(sb.git(repo, "tag", "--list")); tags != "" {
		t.Fatalf("the read-only worker wrote the repository: tags %q", tags)
	}
}
