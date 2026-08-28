package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var npmPinPattern = regexp.MustCompile(`npm@([0-9]+\.[0-9]+\.[0-9]+)`)

// Reads the exact version release.yaml's npm-publish job pins, so a test exercising real
// npm can check it is running against the same version CI does.
func pinnedNpmVersion(t *testing.T) string {
	t.Helper()
	job, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	pin := workflowStep(t, job.Steps, "Pin npm")
	m := npmPinPattern.FindStringSubmatch(pin.Run)
	if m == nil {
		t.Fatalf("Pin npm step run = %q, want an npm@X.Y.Z version to extract", pin.Run)
	}
	return m[1]
}

// npm's --json output shapes have changed between versions with no notice (npm pack
// --json moved from an array to an object keyed by package name); a mismatch here must
// fail loudly rather than silently trust the wrong shape's assumptions.
func requirePinnedNpmVersion(t *testing.T) {
	t.Helper()
	want := pinnedNpmVersion(t)
	out, err := exec.Command("npm", "--version").Output()
	if err != nil {
		t.Fatalf("npm --version: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("npm on PATH is %s, release.yaml pins %s - install the pinned version before trusting this run's npm --json shape assumptions", got, want)
	}
}

func loadWorkflowJobs(t *testing.T, filename string) map[string]workflowJobDef {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", filename))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Jobs map[string]workflowJobDef `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	return document.Jobs
}

func loadReleaseWorkflowJobs(t *testing.T) map[string]workflowJobDef {
	t.Helper()
	return loadWorkflowJobs(t, "release.yaml")
}

// The approval gate only holds if it is wired exactly right: a named GitHub environment
// a required reviewer can pause on, the same release_created condition build already
// uses, and no more token scope than publishing needs.
func TestNpmPublishJobIsGatedForOperatorApproval(t *testing.T) {
	job, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	if job.Environment != "npm-publish" {
		t.Fatalf("npm-publish job environment = %#v, want the literal environment name %q", job.Environment, "npm-publish")
	}
	if got, want := job.If, "needs.release-please.outputs.release_created == 'true'"; got != want {
		t.Fatalf("npm-publish job if = %q, want %q", got, want)
	}
	needs := workflowJobNeeds(t, job.Needs)
	for _, want := range []string{"release-please", "build"} {
		if !containsString(needs, want) {
			t.Fatalf("npm-publish needs = %v, want it to include %q", needs, want)
		}
	}
	wantPermissions := map[string]string{"contents": "read", "id-token": "write"}
	if len(job.Permissions) != len(wantPermissions) {
		t.Fatalf("npm-publish permissions = %v, want exactly %v", job.Permissions, wantPermissions)
	}
	for scope, level := range wantPermissions {
		if job.Permissions[scope] != level {
			t.Fatalf("npm-publish permissions[%s] = %q, want %q", scope, job.Permissions[scope], level)
		}
	}
	if got, want := workflowValue(t, job.Steps, "checkout", "ref"), "${{ needs.release-please.outputs.sha }}"; got != want {
		t.Fatalf("npm-publish checkout ref = %q, want %q", got, want)
	}
}

// A GitHub release must not go non-draft while npm publication for the same release is
// still pending approval or has failed - an otherwise-complete-looking GitHub release
// with no matching npm package would be a silent partial release.
func TestPublishJobWaitsForNpmPublish(t *testing.T) {
	job, ok := loadReleaseWorkflowJobs(t)["publish"]
	if !ok {
		t.Fatal("release workflow has no publish job")
	}
	needs := workflowJobNeeds(t, job.Needs)
	if !containsString(needs, "npm-publish") {
		t.Fatalf("publish needs = %v, want it to include npm-publish", needs)
	}
}

// The runner image's ambient Node/npm is not trustworthy across time; both must be
// pinned explicitly rather than left to whatever version happens to be preinstalled.
func TestNpmPublishJobPinsItsNodeAndNpmToolchain(t *testing.T) {
	job, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	if got, want := setupNodeVersion(t, job.Steps), "24"; got != want {
		t.Fatalf("setup-node node-version = %q, want %q", got, want)
	}
	pin := workflowStep(t, job.Steps, "Pin npm")
	if !strings.Contains(pin.Run, "npm install -g npm@12.0.2") {
		t.Fatalf("Pin npm step run = %q, want an explicit npm@12.0.2 pin", pin.Run)
	}
}

// CI runs the same npm-dependent tests the release job's own packaging relies on; a
// toolchain drift between the two would let CI pass against a version release never sees.
func TestCIAndReleaseWorkflowsPinTheSameNodeAndNpmToolchain(t *testing.T) {
	releaseJob, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	ciJob, ok := loadWorkflowJobs(t, "ci.yaml")["test"]
	if !ok {
		t.Fatal("ci workflow has no test job")
	}
	if got, want := setupNodeVersion(t, releaseJob.Steps), setupNodeVersion(t, ciJob.Steps); got != want {
		t.Fatalf("release npm-publish node-version = %q, ci test node-version = %q, want them equal", got, want)
	}
	releasePin := workflowStep(t, releaseJob.Steps, "Pin npm")
	ciPin := workflowStep(t, ciJob.Steps, "Pin npm")
	if releasePin.Run != ciPin.Run {
		t.Fatalf("release Pin npm run = %q, ci Pin npm run = %q, want them equal", releasePin.Run, ciPin.Run)
	}
}

func setupNodeVersion(t *testing.T, steps []workflowStepDef) string {
	t.Helper()
	for _, step := range steps {
		if strings.Contains(step.Uses, "actions/setup-node@") {
			v, _ := step.With["node-version"].(string)
			return v
		}
	}
	t.Fatal("no actions/setup-node step found")
	return ""
}

func TestNpmPublishJobGeneratesFromTheExactReleaseCommit(t *testing.T) {
	job, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	step := workflowStep(t, job.Steps, "Generate npm packages for this release")
	for _, want := range []string{
		`test "$(git rev-parse HEAD)" = "$commit"`,
		"generate.sh --version",
		"--commit",
	} {
		if !strings.Contains(step.Run, want) {
			t.Fatalf("generate step run = %q, want it to contain %q", step.Run, want)
		}
	}
}

// The npm-qualified target set must never be written down in the workflow itself - the
// same invariant tests/release/npm_generate_test.go proves for generate.sh. A future edit
// that hardcodes a platform key here must fail even if the job still works today.
func TestNpmPublishStepNeverHardcodesATargetList(t *testing.T) {
	job, ok := loadReleaseWorkflowJobs(t)["npm-publish"]
	if !ok {
		t.Fatal("release workflow has no npm-publish job")
	}
	step := workflowStep(t, job.Steps, "Publish every runtime-qualified platform package, then the meta package")
	if !strings.Contains(step.Run, "generate.sh --print-targets") {
		t.Fatalf("publish step run = %q, want it to derive targets via generate.sh --print-targets", step.Run)
	}
	for _, banned := range []string{"linux-x64", "win32-x64", "darwin-x64", "darwin-arm64", "linux-arm64"} {
		if strings.Contains(step.Run, banned) {
			t.Fatalf("publish step run contains hardcoded target %q; the target set must be derived, never listed", banned)
		}
	}
	if step.Env["NPM_BOOTSTRAP_TOKEN"] != "${{ secrets.NPM_BOOTSTRAP_TOKEN }}" {
		t.Fatalf("publish step env NPM_BOOTSTRAP_TOKEN = %q, want the secret reference", step.Env["NPM_BOOTSTRAP_TOKEN"])
	}
}

// Executes the workflow step's own shell text for real, with only the publish-target
// script faked (that state machine is proven end to end in tests/npmpublish), to prove
// platforms publish before meta in exactly the order generate.sh derives.
func TestNpmPublishStepOrdersPlatformsBeforeMeta(t *testing.T) {
	testNpmPublishStepOrdering(t, false)
}

// A chained "producer | jq -r" here can hand back CRLF-terminated lines (observed on a
// Windows runner): the target loop and the packed name/tarball/integrity all read jq
// output that way, so a hostile jq proves all four survive it at once.
func TestNpmPublishStepToleratesCRLFTerminatedToolOutput(t *testing.T) {
	testNpmPublishStepOrdering(t, true)
}

func testNpmPublishStepOrdering(t *testing.T, hostileJQ bool) {
	requireTools(t, "jq", "git", "npm")
	requirePinnedNpmVersion(t)
	fixture := copyNpmPackagingFixture(t)
	const version = "0.0.0-workfloworder"
	generateReal(t, fixture, version)

	targets := runGenerateJSONTargets(t, filepath.Join(fixture.dir, "packaging", "npm", "generate.sh"), nil)
	if len(targets) == 0 {
		t.Fatal("no runtime-qualified targets to order")
	}

	logPath := filepath.Join(t.TempDir(), "calls.log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> '" + logPath + "'\nprintf 'token=%s\\n' \"$NPM_BOOTSTRAP_TOKEN\" >> '" + logPath + "'\n"
	scriptPath := filepath.Join(fixture.dir, ".github", "scripts", "npm-publish-target.sh")
	if err := os.WriteFile(scriptPath, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}

	job := loadReleaseWorkflowJobs(t)["npm-publish"]
	step := workflowStep(t, job.Steps, "Publish every runtime-qualified platform package, then the meta package")
	run := strings.ReplaceAll(step.Run, "${{ needs.release-please.outputs.version }}", version)

	path := os.Getenv("PATH")
	if hostileJQ {
		fakeBin := t.TempDir()
		installCRLFJQWrapper(t, fakeBin)
		path = fakeBin + string(os.PathListSeparator) + path
	}
	cmd := exec.Command("bash", "-c", run)
	cmd.Dir = fixture.dir
	cmd.Env = envWithPath(path, "NPM_BOOTSTRAP_TOKEN=fixture-token")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("publish step: %v: %s", err, out)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if want := 2 * (len(targets) + 1); len(lines) != want {
		t.Fatalf("publish-target invocations = %d lines (%v), want %d (name+token per target, plus meta)", len(lines), lines, want)
	}
	for i, target := range targets {
		if got, want := lines[2*i], "@atqamz/hand-"+target; got != want {
			t.Fatalf("invocation %d package = %q, want %q in generate.sh's derived order", i, got, want)
		}
		if got := lines[2*i+1]; got != "token=fixture-token" {
			t.Fatalf("invocation %d token line = %q, want the bootstrap token visible to the state machine", i, got)
		}
	}
	if got, want := lines[2*len(targets)], "@atqamz/hand"; got != want {
		t.Fatalf("final invocation package = %q, want the meta package %q published last", got, want)
	}
}
