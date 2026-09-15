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

func TestMutationWorkflowGatesCheapPackagesAndReportsExpensiveOnes(t *testing.T) {
	jobs := loadWorkflowJobs(t, "ci.yaml")
	cheap, ok := jobs["mutation"]
	if !ok {
		t.Fatal("ci workflow has no always-on mutation job")
	}
	expensive, ok := jobs["mutation-expensive"]
	if !ok {
		t.Fatal("ci workflow has no expensive mutation job")
	}
	if expensive.ContinueOnError != true {
		t.Fatal("expensive mutation job must stay non-blocking")
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, packagePath := range []string{
		"./internal/shellquote",
		"./internal/age",
		"./internal/axi",
		"./internal/registry",
		"./internal/completion",
	} {
		if !strings.Contains(workflow, packagePath) {
			t.Errorf("always-on mutation job does not name %s", packagePath)
		}
	}
	for _, packagePath := range []string{"./internal/store", "./internal/runtime"} {
		if !strings.Contains(workflow, packagePath) {
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
	if !strings.Contains(workflow, "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0") {
		t.Errorf("mutation workflow does not pin gremlins v0.6.0")
	}
	for _, evidence := range []string{"source: $(git rev-parse HEAD)", "tags: test", "package: ${{ matrix.package }}", "elapsed_time"} {
		if !strings.Contains(workflow, evidence) {
			t.Errorf("mutation workflow does not report %q", evidence)
		}
	}
	if strings.Contains(workflow, "name: mutation-${{ matrix.package }}") {
		t.Error("artifact names cannot contain package-path slashes")
	}
}

func TestMutationExpensiveDispatchRequiresExplicitOptIn(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		On struct {
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Default  *bool  `yaml:"default"`
					Required bool   `yaml:"required"`
					Type     string `yaml:"type"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
		Jobs map[string]workflowJobDef `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse ci.yaml: %v", err)
	}
	input, ok := document.On.WorkflowDispatch.Inputs["run_expensive_mutation"]
	if !ok {
		t.Fatal("ci workflow dispatch has no run_expensive_mutation input")
	}
	if input.Required {
		t.Fatal("run_expensive_mutation must remain optional")
	}
	if input.Default == nil || *input.Default {
		t.Fatal("run_expensive_mutation default must explicitly remain false")
	}
	if input.Type != "boolean" {
		t.Fatalf("run_expensive_mutation type = %q, want boolean", input.Type)
	}
	expensive, ok := document.Jobs["mutation-expensive"]
	if !ok {
		t.Fatal("ci workflow has no expensive mutation job")
	}
	if got, want := expensive.If, "github.event_name == 'schedule' || inputs.run_expensive_mutation"; got != want {
		t.Fatalf("expensive mutation job if = %q, want %q", got, want)
	}
}
