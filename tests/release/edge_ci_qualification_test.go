package release

import (
	"strings"
	"testing"
)

func TestEdgePublicationWaitsForExactMainCI(t *testing.T) {
	jobs := loadWorkflowJobs(t, "edge.yaml")
	qualify, ok := jobs["qualify"]
	if !ok {
		t.Fatal("edge workflow has no exact-source CI qualification job")
	}
	if !containsString(workflowJobNeeds(t, qualify.Needs), "prepare") ||
		qualify.If != "needs.prepare.outputs.eligible == 'true'" {
		t.Fatalf("edge qualification is not bound to eligible candidate: %#v", qualify)
	}
	if qualify.Permissions["actions"] != "read" || qualify.Permissions["contents"] != "read" {
		t.Fatalf("qualification permissions = %v, want read-only Actions and contents", qualify.Permissions)
	}
	if got := workflowValue(t, qualify.Steps, "checkout", "ref"); got != "${{ inputs.sha }}" {
		t.Fatalf("qualification checkout ref = %q", got)
	}
	step := workflowStep(t, qualify.Steps, "Qualify exact main CI")
	if step.Env["EDGE_SHA"] != "${{ inputs.sha }}" ||
		step.Env["GH_TOKEN"] != "${{ github.token }}" ||
		!strings.Contains(step.Run, `.github/scripts/qualify-ci.sh "$EDGE_SHA"`) {
		t.Fatalf("qualification step does not inspect exact edge SHA: %#v", step)
	}
	for _, name := range []string{"build", "publish"} {
		if !containsString(workflowJobNeeds(t, jobs[name].Needs), "qualify") {
			t.Fatalf("edge %s can run without exact CI qualification", name)
		}
	}
}
