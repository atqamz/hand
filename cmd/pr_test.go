package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
)

func assertExitCode2(t *testing.T, err error) {
	t.Helper()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("got %v, want ExitError code 2", err)
	}
}

func addOriginRemote(t *testing.T, dir, url string) {
	t.Helper()
	c := exec.Command("git", "remote", "add", "origin", url)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git remote add origin failed: %v: %s", err, out)
	}
}

// The diagnostic real gh answers with for a pull request that is not there (FIDELITY.md). A fake that
// paraphrases it makes the absent path unreachable, and every refusal below an unknown one instead.
func ghPRAbsentDiagnostic(number int) string {
	return fmt.Sprintf("GraphQL: Could not resolve to a PullRequest with the number of %d. (repository.pullRequest)\n", number)
}

func writeFakeGhPRView(t *testing.T, exitCode int) {
	t.Helper()
	response := faketool.GHResponse{Command: "pr view", Exit: exitCode, Stderr: ghPRAbsentDiagnostic(1)}
	if exitCode == 0 {
		response = faketool.GHResponse{Command: "pr view", Stdout: "{\"state\":\"OPEN\"}"}
	}
	writeFakeGhPRViewResponse(t, response)
}

func writeFakeGhPRViewResponse(t *testing.T, response faketool.GHResponse) {
	t.Helper()
	faketool.GH{Responses: []faketool.GHResponse{response}}.Install(t, faketool.Bin(t))
}

// Backs a successful `hand pr` recording with a real per-PR fake, not just a canned "pr view"
// response: recording also reasserts operator-owned metadata, which needs pr edit and pr ready
// to resolve against something, not just answer state.
func installFakeGhPRs(t *testing.T, urls ...string) {
	t.Helper()
	prs := make([]faketool.GHPR, len(urls))
	for i, url := range urls {
		prs[i] = faketool.GHPR{URL: url, State: "OPEN"}
	}
	faketool.GH{PRs: prs}.Install(t, faketool.Bin(t))
}

func setupPRHome(t *testing.T) (home, clonePath string) {
	t.Helper()
	home = t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	clonePath = filepath.Join(home, "projects", "demo")
	initGitRepo(t, clonePath)
	return home, clonePath
}

func TestPRRejectsMalformedURL(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://example.com/not/github"})
	err := cmd.Execute()
	assertExitCode2(t, err)
}

func TestPRRefusesWhenTaskMissing(t *testing.T) {
	setupPRHome(t)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"missing-task", "https://github.com/a/b/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

func TestPRRefusesDifferentAlreadyRecordedPR(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", PR: "https://github.com/a/b/pull/1"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/a/b/pull/2"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/a/b/pull/1" {
		t.Fatalf("task.PR = %q, want the original PR left untouched", task.PR)
	}
}

// Pins the reconciling repeat: an operator retrying this command after the URL already made it into task
// state gets a friendly no-op instead of an error.
func TestPRReconcilesWhenSameURLAlreadyRecorded(t *testing.T) {
	home, _ := setupPRHome(t)
	url := "https://github.com/a/b/pull/1"
	// The project is deliberately unregistered: reaching validation would exit 3, so passing also proves it
	// is skipped for a URL already on record. Reasserting metadata does not depend on project
	// registration, so it still runs and still needs a PR to resolve against.
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered", PR: url}); err != nil {
		t.Fatal(err)
	}
	installFakeGhPRs(t, url)

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", url})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: already-recorded\n") {
		t.Fatalf("out = %q, want an already-recorded message", out.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != url {
		t.Fatalf("task.PR = %q, want %q left in place", task.PR, url)
	}
}

func TestPRRefusesWhenProjectNotRegistered(t *testing.T) {
	home, _ := setupPRHome(t)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "unregistered"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/a/b/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

func TestPRRefusesWhenRepoMismatch(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/other-repo.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/other-repo.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
}

func TestPRAcceptsRenamedRepositoryAfterProjectSetURL(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	home, _ := setupRegisteredURLProject(t, oldURL)
	if err := project.SetUpstream(home, "secondhand", ""); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "secondhand"}); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/atqamz/hand/pull/185"
	faketool.GH{PRs: []faketool.GHPR{{Number: 185, URL: prURL, Repo: "atqamz/hand"}}}.Install(t, faketool.Bin(t))

	before := newPRCmd()
	before.SetArgs([]string{"task-1", prURL})
	if err := before.Execute(); err == nil || !strings.Contains(err.Error(), "belongs to atqamz/hand") {
		t.Fatalf("before set-url error = %v, want stale-origin refusal", err)
	}

	if err := executeProjectSetURL(t, home, newURL); err != nil {
		t.Fatal(err)
	}
	proj, exists, err := project.Find(home, "secondhand")
	if err != nil || !exists {
		t.Fatalf("project.Find = %+v, %v, %v", proj, exists, err)
	}
	if got, err := project.RepoSlug(home, proj); err != nil || got != "atqamz/hand" {
		t.Fatalf("RepoSlug = %q, %v, want renamed repo", got, err)
	}

	after := newPRCmd()
	after.SetArgs([]string{"task-1", prURL})
	if err := after.Execute(); err != nil {
		t.Fatalf("after set-url error = %v, want PR accepted: %v", err, err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != prURL {
		t.Fatalf("task.PR = %q, want %q", task.PR, prURL)
	}
}

func TestDetectPRSearchesRenamedRepositoryAfterProjectSetURL(t *testing.T) {
	oldURL := "https://github.com/atqamz/secondhand.git"
	newURL := "https://github.com/atqamz/hand.git"
	home, clonePath := setupRegisteredURLProject(t, oldURL)
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "secondhand"}, state.Attempt{Lifecycle: state.AttemptRunning, Worktree: clonePath}); err != nil {
		t.Fatal(err)
	}
	if err := executeProjectSetURL(t, home, newURL); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/atqamz/hand/pull/185"
	faketool.GH{PRs: []faketool.GHPR{{
		Number: 185, URL: prURL, Branch: "main", Repo: "atqamz/hand",
	}}}.Install(t, faketool.Bin(t))

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	proj, exists, err := project.Find(home, "secondhand")
	if err != nil || !exists {
		t.Fatalf("project.Find = %+v, %v, %v", proj, exists, err)
	}
	got, observation, err := runtime.DetectPR(context.Background(), home, task, state.Attempt{Worktree: clonePath}, proj)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the PR found", observation)
	}
	if got.PR != prURL {
		t.Fatalf("detected task.PR = %q, want %q", got.PR, prURL)
	}
}

// A fork contribution's PR lives on the upstream repo, never on the fork hand pushed to, so the guard has
// to accept the declared upstream - and only that one.
func TestPRAcceptsTheDeclaredUpstreamAndStillRefusesAnyOtherRepo(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/atqamz/no-mistakes.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "demo", "kunchenguid/no-mistakes"); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-2", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	upstreamPR := "https://github.com/kunchenguid/no-mistakes/pull/597"
	installFakeGhPRs(t, upstreamPR)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", upstreamPR})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("recording a PR on the declared upstream: %v", err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != upstreamPR {
		t.Fatalf("task.PR = %q, want %q", task.PR, upstreamPR)
	}

	// The pair is kept in one test so the accepting and the refusing case share an identical project,
	// leaving the declaration as the only difference between them.
	unrelated := newPRCmd()
	unrelated.SetArgs([]string{"task-2", "https://github.com/someone/else/pull/1"})
	assertExitCode3(t, unrelated.Execute())
	other, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if other.PR != "" {
		t.Fatalf("task.PR = %q, want a repo that is neither the project's nor its upstream refused", other.PR)
	}
}

// A GitHub slug is case-insensitive, so the repo guard has to fold: a PR URL carries GitHub's canonical
// casing while the slug it is checked against comes from whatever casing the clone's origin remote and the
// declared upstream were written in.
func TestPRAcceptsCanonicalCasingForDifferentlyCasedRemoteAndUpstream(t *testing.T) {
	home, clonePath := setupPRHome(t)
	// Comparing exactly refuses landed work as a foreign repo, and on hand teardown's detection path that
	// surfaces as "no PR recorded" - unlanded, the opposite of what happened.
	addOriginRemote(t, clonePath, "https://github.com/Atqamz/No-Mistakes.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/Atqamz/No-Mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "demo", "KunchenGUID/No-Mistakes"); err != nil {
		t.Fatal(err)
	}
	// Both sides of the guard are covered in one test so the fold is pinned for the project's own repo and
	// its upstream alike.
	for _, id := range []string{"task-1", "task-2"} {
		if err := state.Write(home, state.Task{ID: id, Project: "demo"}); err != nil {
			t.Fatal(err)
		}
	}
	own := "https://github.com/atqamz/no-mistakes/pull/31"
	upstreamPR := "https://github.com/kunchenguid/no-mistakes/pull/597"
	installFakeGhPRs(t, own, upstreamPR)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", own})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want the project's own repo accepted in GitHub's canonical casing", err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != own {
		t.Fatalf("task.PR = %q, want %q", task.PR, own)
	}

	up := newPRCmd()
	up.SetArgs([]string{"task-2", upstreamPR})
	if err := up.Execute(); err != nil {
		t.Fatalf("got %v, want the declared upstream accepted in GitHub's canonical casing", err)
	}
	other, err := state.Read(home, "task-2")
	if err != nil {
		t.Fatal(err)
	}
	if other.PR != upstreamPR {
		t.Fatalf("task.PR = %q, want %q", other.PR, upstreamPR)
	}
}

func TestPRRefusesWhenGhReportsNotFound(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 1)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want no PR recorded when gh can't confirm it exists", task.PR)
	}
}

// A rejected credential is not a missing PR: reporting one as the other sends the operator to fix a URL
// that was right all along, which is the confusion atqamz/hand#241 removes.
func TestPRRefusesWithoutClaimingAbsenceWhenGhCannotAnswer(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRViewResponse(t, faketool.GHResponse{
		Command: "pr view",
		Stderr:  "gh: HTTP 401: Bad credentials (https://api.github.com/graphql)\n",
		Exit:    1,
	})

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want a refusal that does not report the PR as absent", err)
	}
	if !strings.Contains(err.Error(), "could not be observed") {
		t.Fatalf("got err %v, want the refusal to name the observation that did not complete", err)
	}
	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want no PR recorded from an observation that never completed", task.PR)
	}
}

// A PR number that does not exist on the declared upstream has to send the
// operator to the upstream, not to the project's own repo: naming the repo the
// URL does not belong to points the fix at the wrong place.
func TestPRNotFoundOnUpstreamNamesTheUpstreamRepo(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/atqamz/no-mistakes.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/atqamz/no-mistakes.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := project.SetUpstream(home, "demo", "kunchenguid/no-mistakes"); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 1)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/kunchenguid/no-mistakes/pull/597"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "not found in kunchenguid/no-mistakes") {
		t.Fatalf("got err %v, want the refusal to name the upstream the URL belongs to", err)
	}
	if strings.Contains(err.Error(), "not found in atqamz/no-mistakes") {
		t.Fatalf("got err %v, want the project's own repo not named as the missing PR's repo", err)
	}
}

// atqamz/hand#423: the refusal for a sibling repo has to point at the real escape (--cross-repo),
// not at declaring an upstream - that would put false fork topology in the registry to record one
// task's PR.
func TestPRRefusesSiblingRepoWithoutCrossRepoFlagAndNamesTheRealEscape(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/yes2games/yes2infra.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/yes2games/yes2infra.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/yes2games/butler/pull/92"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if strings.Contains(err.Error(), "no upstream is declared") {
		t.Fatalf("got %v, want the refusal to no longer offer declaring an upstream as the escape", err)
	}
	if !strings.Contains(err.Error(), "--cross-repo") || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("got %v, want the refusal to name --cross-repo and --reason as the real escape", err)
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want nothing recorded without the opt-in", task.PR)
	}
}

func TestPRCrossRepoWithoutReasonRefused(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/yes2games/yes2infra.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/yes2games/yes2infra.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "--cross-repo", "https://github.com/yes2games/butler/pull/92"})
	err := cmd.Execute()
	assertExitCode2(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want nothing recorded without a reason", task.PR)
	}
}

// --reason answers a question --cross-repo never asked, so it is refused rather than silently
// ignored: a same-repo record carrying a reason nobody asked for would be confusing to read back.
func TestPRReasonWithoutCrossRepoRefused(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "--reason", "moved upstream", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	assertExitCode2(t, err)
}

// The case that motivated atqamz/hand#423: the component moved to a sibling repository between
// brief and delivery, and the worker followed the code there.
func TestPRRecordsSiblingRepoWithCrossRepoFlagAndReason(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/yes2games/yes2infra.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/yes2games/yes2infra.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	crossRepoPR := "https://github.com/yes2games/butler/pull/92"
	installFakeGhPRs(t, crossRepoPR)

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	reason := "component moved to yes2games/butler before the worker started; see ADR-0015"
	cmd.SetArgs([]string{"task-1", "--cross-repo", "--reason", reason, crossRepoPR})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("recording a deliberate cross-repo PR: %v", err)
	}
	if !strings.Contains(out.String(), "result: recorded\n") || !strings.Contains(out.String(), "cross_repo_reason: "+reason+"\n") {
		t.Fatalf("out = %q, want the recorded confirmation to carry the reason", out.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != crossRepoPR {
		t.Fatalf("task.PR = %q, want %q", task.PR, crossRepoPR)
	}
	if task.PRCrossRepoReason != reason {
		t.Fatalf("task.PRCrossRepoReason = %q, want %q", task.PRCrossRepoReason, reason)
	}
}

// --cross-repo waives exactly the repo-match check, nothing else: a PR that does not exist on
// GitHub is still refused.
func TestPRCrossRepoStillRefusesAPRThatDoesNotExistOnGitHub(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/yes2games/yes2infra.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/yes2games/yes2infra.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	writeFakeGhPRView(t, 1)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "--cross-repo", "--reason", "moved", "https://github.com/yes2games/butler/pull/92"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "" {
		t.Fatalf("task.PR = %q, want nothing recorded for a PR gh cannot confirm exists", task.PR)
	}
}

// Write-once holds regardless of the opt-in: a cross-repo record does not get a second chance to
// point somewhere else, cross-repo or not.
func TestPRCrossRepoWriteOnceRefusesASecondDifferentURL(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/yes2games/yes2infra.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/yes2games/yes2infra.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	first := "https://github.com/yes2games/butler/pull/92"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo", PR: first, PRCrossRepoReason: "moved to butler"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "--cross-repo", "--reason", "moved again", "https://github.com/yes2games/butler/pull/93"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != first {
		t.Fatalf("task.PR = %q, want the original cross-repo PR left untouched", task.PR)
	}
}

func TestPRRecordsSuccessfully(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	installFakeGhPRs(t, "https://github.com/owner/secondhand/pull/1")

	cmd := newPRCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: recorded\n") || !strings.Contains(out.String(), "/owner/secondhand/pull/1") {
		t.Fatalf("out = %q, want a recorded confirmation", out.String())
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/owner/secondhand/pull/1" {
		t.Fatalf("task.PR = %q, want the URL recorded", task.PR)
	}
}

func TestPREstablishesAPipelineRegionOnTheLiveBody(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/owner/secondhand/pull/1"
	faketool.GH{PRs: []faketool.GHPR{{URL: url, State: "OPEN", Body: "operator wrote this"}}}.Install(t, faketool.Bin(t))

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", url})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	meta, observation := ghutil.FetchPRMetadata(context.Background(), url)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the recorded PR found", observation)
	}
	operatorBody, _, ok := ghutil.SplitBody(meta.Body)
	if !ok {
		t.Fatalf("body = %q, want hand pr to have established a pipeline region", meta.Body)
	}
	if operatorBody != "operator wrote this" {
		t.Fatalf("operator body = %q, want the original content preserved", operatorBody)
	}
}

func TestPRPropagatesAReassertMetadataFailure(t *testing.T) {
	home, clonePath := setupPRHome(t)
	addOriginRemote(t, clonePath, "https://github.com/owner/secondhand.git")
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://github.com/owner/secondhand.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	// This view response answers validation but registers no PR for the fake to resolve the
	// edit that establishing a pipeline region needs against, so reasserting fails after
	// recordPR has already succeeded.
	writeFakeGhPRView(t, 0)

	cmd := newPRCmd()
	cmd.SetArgs([]string{"task-1", "https://github.com/owner/secondhand/pull/1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "reassert operator-owned PR metadata") {
		t.Fatalf("got %v, want the reassert failure to propagate", err)
	}

	task, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if task.PR != "https://github.com/owner/secondhand/pull/1" {
		t.Fatalf("task.PR = %q, want the URL recorded even though reasserting its metadata failed", task.PR)
	}
}
