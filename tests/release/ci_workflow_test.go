package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCIPushCannotPublish(t *testing.T) {
	jobs := loadWorkflowJobs(t, "ci.yaml")
	if job, ok := jobs["edge"]; ok {
		t.Fatalf("ci workflow has edge publication job: %#v", job)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse ci.yaml: %v", err)
	}
	if document.Permissions["contents"] != "read" {
		t.Fatalf("ci workflow permissions[contents] = %q, want read", document.Permissions["contents"])
	}
	for scope, level := range document.Permissions {
		if level == "write" {
			t.Fatalf("ci workflow permissions[%s] = write", scope)
		}
	}
	for name, job := range document.Jobs {
		if job.Uses == "./.github/workflows/edge.yaml" {
			t.Fatalf("ci workflow job %q invokes edge publisher", name)
		}
	}
	for name, job := range jobs {
		for scope, level := range job.Permissions {
			if level == "write" {
				t.Fatalf("ci workflow job %q grants %s: write", name, scope)
			}
		}
	}
}

func TestCIFastPRKeepsFullReleaseAndMainMatrix(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Jobs map[string]struct {
			If       string `yaml:"if"`
			Strategy struct {
				Matrix struct {
					OS any `yaml:"os"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	matrix, ok := document.Jobs["test"].Strategy.Matrix.OS.(string)
	if !ok {
		t.Fatalf("test matrix = %#v, want event-scoped expression", document.Jobs["test"].Strategy.Matrix.OS)
	}
	for _, part := range []string{
		"github.event_name == 'pull_request'",
		"github.head_ref != 'release-please--branches--main'",
		`'["ubuntu-latest"]'`,
		"ubuntu-24.04-arm", "macos-latest", "macos-15-intel", "windows-latest",
	} {
		if !strings.Contains(matrix, part) {
			t.Errorf("test matrix %q missing %q", matrix, part)
		}
	}
	for _, job := range []string{"e2e-windows", "e2e-macos", "e2e-macos-intel"} {
		if got := document.Jobs[job].If; got != "github.event_name != 'pull_request' || github.head_ref == 'release-please--branches--main'" {
			t.Errorf("%s if = %q, want main and release PR", job, got)
		}
	}
	if got := document.Jobs["e2e"].If; got != "" {
		t.Errorf("Linux E2E if = %q, want PR and main", got)
	}
}

func TestMutationWorkflowsGateCheapPackagesAndReportExpensiveOnes(t *testing.T) {
	ciJobs := loadWorkflowJobs(t, "ci.yaml")
	cheap, ok := ciJobs["mutation"]
	if !ok {
		t.Fatal("ci workflow has no always-on mutation job")
	}
	expensive, ok := loadWorkflowJobs(t, "mutation-expensive.yaml")["mutation-expensive"]
	if !ok {
		t.Fatal("dedicated workflow has no expensive mutation job")
	}
	if expensive.ContinueOnError != true {
		t.Fatal("expensive mutation job must stay non-blocking")
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ciWorkflow := string(data)
	if strings.Contains(ciWorkflow, "schedule:") {
		t.Fatal("ci workflow must not schedule expensive mutation evidence")
	}
	if !strings.Contains(ciWorkflow, "uses: ./.github/workflows/mutation-expensive.yaml") {
		t.Fatal("ci workflow has no manual caller for expensive mutation evidence")
	}
	expensiveData, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "mutation-expensive.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	expensiveWorkflow := string(expensiveData)
	for _, packagePath := range []string{
		"./internal/shellquote",
		"./internal/age",
		"./internal/axi",
		"./internal/registry",
		"./internal/completion",
	} {
		if !strings.Contains(ciWorkflow, packagePath) {
			t.Errorf("always-on mutation job does not name %s", packagePath)
		}
	}
	for _, packagePath := range []string{"./internal/store", "./internal/runtime"} {
		if !strings.Contains(expensiveWorkflow, packagePath) {
			t.Errorf("expensive mutation job does not name %s", packagePath)
		}
	}
	for name, job := range map[string]workflowJobDef{"mutation": cheap, "mutation-expensive": expensive} {
		for scope, level := range job.Permissions {
			if level == "write" {
				t.Errorf("%s grants %s: write", name, scope)
			}
		}
	}
	if !strings.Contains(ciWorkflow, "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0") ||
		!strings.Contains(expensiveWorkflow, "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0") {
		t.Error("mutation workflows do not pin gremlins v0.6.0")
	}
	for _, evidence := range []string{"source: $(git rev-parse HEAD)", "tags: test", "package: ${{ matrix.package }}", "elapsed_time"} {
		if !strings.Contains(ciWorkflow, evidence) || !strings.Contains(expensiveWorkflow, evidence) {
			t.Errorf("mutation workflows do not report %q", evidence)
		}
	}
	if !strings.Contains(expensiveWorkflow, "[.files[].mutations[] | select(.status == \"TIMED OUT\")] | length") {
		t.Error("expensive mutation workflow does not summarize timed-out mutants")
	}
	if strings.Contains(ciWorkflow, "name: mutation-${{ matrix.package }}") ||
		strings.Contains(expensiveWorkflow, "name: mutation-expensive-${{ matrix.package }}") {
		t.Error("artifact names cannot contain package-path slashes")
	}
}

func TestMutationExpensiveWorkflowSchedulesAndPermitsManualRuns(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "mutation-expensive.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Jobs map[string]workflowJobDef `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse ci.yaml: %v", err)
	}
	if !strings.Contains(string(data), "workflow_dispatch:") {
		t.Fatal("expensive mutation workflow must permit manual runs")
	}
	expensive, ok := document.Jobs["mutation-expensive"]
	if !ok {
		t.Fatal("expensive mutation workflow has no expensive mutation job")
	}
	if expensive.If != "" {
		t.Fatalf("expensive mutation job if = %q, want no condition", expensive.If)
	}
	if !strings.Contains(string(data), "schedule:") {
		t.Fatal("expensive mutation workflow has no schedule")
	}
}
