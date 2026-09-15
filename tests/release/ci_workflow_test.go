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
