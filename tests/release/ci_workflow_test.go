package release

import (
	"os"
	"path/filepath"
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
