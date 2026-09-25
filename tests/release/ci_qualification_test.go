package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePublicationWaitsForExactMainCI(t *testing.T) {
	jobs := loadReleaseWorkflowJobs(t)
	qualify, ok := jobs["qualify"]
	if !ok {
		t.Fatal("release workflow has no exact-source CI qualification job")
	}
	if qualify.If != "needs.release-please.outputs.release_created == 'true'" ||
		!containsString(workflowJobNeeds(t, qualify.Needs), "release-please") {
		t.Fatalf("qualification gate is not bound to release creation: %#v", qualify)
	}
	if qualify.Permissions["actions"] != "read" || qualify.Permissions["contents"] != "read" {
		t.Fatalf("qualification permissions = %v, want read-only Actions and contents", qualify.Permissions)
	}
	if qualify.TimeoutMinutes != 0 {
		t.Fatalf("qualification timeout = %d minutes, want the job default so it outlasts the CI jobs it watches", qualify.TimeoutMinutes)
	}
	if got := workflowValue(t, qualify.Steps, "checkout", "ref"); got != "${{ needs.release-please.outputs.sha }}" {
		t.Fatalf("qualification checkout ref = %q", got)
	}
	step := workflowStep(t, qualify.Steps, "Qualify exact main CI")
	if step.Env["RELEASE_SHA"] != "${{ needs.release-please.outputs.sha }}" ||
		step.Env["GH_TOKEN"] != "${{ github.token }}" ||
		!strings.Contains(step.Run, `.github/scripts/qualify-ci.sh "$RELEASE_SHA"`) {
		t.Fatalf("qualification step does not inspect exact release SHA: %#v", step)
	}
	for _, name := range []string{"build", "npm-publish", "publish"} {
		if !containsString(workflowJobNeeds(t, jobs[name].Needs), "qualify") {
			t.Fatalf("%s can run without exact CI qualification", name)
		}
	}
	if got := jobs["publish"].Environment; got != "github-release" {
		t.Fatalf("publish job environment = %#v, want the operator-approved %q environment", got, "github-release")
	}
}

func TestExactCIQualificationSelectsOnlyMatchingSuccessfulPush(t *testing.T) {
	script := filepath.Join(repoRoot(t), ".github", "scripts", "qualify-ci.sh")
	for _, test := range []struct {
		name      string
		runs      string
		watchExit string
		wantPass  bool
		wantWatch bool
	}{
		{
			name: "exact push", runs: `{"workflow_runs":[{"id":43,"head_sha":"` + releaseCommit + `","event":"pull_request"},{"id":42,"head_sha":"` + releaseCommit + `","event":"push"}]}`,
			watchExit: "0", wantPass: true, wantWatch: true,
		},
		{
			name: "different source", runs: `{"workflow_runs":[{"id":42,"head_sha":"ffffffffffffffffffffffffffffffffffffffff","event":"push"}]}`,
			watchExit: "0",
		},
		{
			name: "failed CI", runs: `{"workflow_runs":[{"id":42,"head_sha":"` + releaseCommit + `","event":"push"}]}`,
			watchExit: "1", wantWatch: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			watched := filepath.Join(bin, "watched")
			gh := `#!/bin/sh
if [ "$1" = api ]; then
  printf '%s\n' "$FAKE_CI_RUNS"
  exit 0
fi
if [ "$1" = run ] && [ "$2" = watch ]; then
  printf '%s\n' "$*" > "$FAKE_WATCHED"
  exit "$FAKE_WATCH_EXIT"
fi
exit 99
`
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, releaseCommit)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "GITHUB_REPOSITORY=atqamz/hand",
				"FAKE_CI_RUNS="+test.runs, "FAKE_WATCHED="+watched, "FAKE_WATCH_EXIT="+test.watchExit)
			out, err := cmd.CombinedOutput()
			if (err == nil) != test.wantPass {
				t.Fatalf("qualification = %v, output %q", err, out)
			}
			args, readErr := os.ReadFile(watched)
			if (readErr == nil) != test.wantWatch {
				t.Fatalf("watch invocation = %q, %v; output %q", args, readErr, out)
			}
			if test.wantWatch && (!strings.Contains(string(args), "run watch 42") || !strings.Contains(string(args), "--exit-status")) {
				t.Fatalf("watched wrong run or ignored its result: %q", args)
			}
		})
	}
}
