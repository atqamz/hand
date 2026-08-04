//go:build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// writeFakeGHAnyCasing fakes `gh pr list` the way GitHub actually serves a repo:
// the same PR list comes back under every casing of the slug, so a search issued
// in the casing a clone's origin remote or a declared upstream happens to carry
// answers exactly as one issued in GitHub's canonical casing. A fake that matched
// --repo case-sensitively would answer a differently-cased search with an empty
// list and hide the very hit these tests are about. prRepoCasings are the casings
// of the one repo that holds the PR; every other repo answers empty.
func writeFakeGHAnyCasing(t *testing.T, dir string, prRepoCasings []string, number int, url, prState, headRepo string) {
	t.Helper()
	item := fmt.Sprintf(`{"number":%d,"url":"%s","state":"%s","headRepository":{"nameWithOwner":"%s"}}`,
		number, url, prState, headRepo)
	patterns := make([]string, len(prRepoCasings))
	for i, casing := range prRepoCasings {
		patterns[i] = fmt.Sprintf("%q", casing)
	}
	caseBody := fmt.Sprintf(`  "pr list") case "$4" in
    %s) printf '[%s]' ;;
    *) printf '[]' ;;
    esac ;;
  "pr view") printf '{"state":"%s"}' ;;`, strings.Join(patterns, "|"), item, prState)
	writeFakeDispatch(t, dir, "gh", "", "$1 $2", caseBody)
}

// setupCasedGateProject registers a project from remoteURL, gives it a task-1
// worktree on a branch, and spawns task-1 with a commit on it - the state a
// no-mistakes gate leaves behind when it opens the PR itself, so t.PR is empty and
// hand has to find the PR by branch. It returns the fake-binary dir so the caller
// can write the gh fake that decides what that search finds.
func setupCasedGateProject(t *testing.T, remoteURL, projectName string) (home, binaries string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, remoteURL, remote)

	binaries = binDir(t)
	worktree := filepath.Join(t.TempDir(), "wt-task-1")
	writeFakeTreehouse(t, binaries, worktree)
	writeFakeHerdrStatic(t, binaries, herdrIDs{WorkspaceID: "ws-1", TabID: "tab-1", PaneID: "pane-1", Label: projectName})

	home = newHome(t)
	added := runHand(t, home, "project", "add", remoteURL, "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	runGitIn(t, filepath.Join(home, "projects", projectName), "worktree", "add", "-q", "-b", "task-1-branch", worktree)

	writeBrief(t, home, "task-1")
	spawned := runHand(t, home, "spawn", "task-1", projectName)
	if spawned.code != 0 {
		t.Fatalf("spawn: exit %d, stderr %q", spawned.code, spawned.stderr)
	}
	runGitIn(t, worktree, "commit", "--allow-empty", "-q", "-m", "fix the flake")
	return home, binaries
}

// TestPRRecordAcceptsCanonicalCasingOfOwnRepoAndUpstream drives the recording
// guard itself: a PR URL carries GitHub's canonical casing while the slugs it is
// checked against come from whatever casing the clone's origin remote and the
// declared upstream were written in. Both have to be accepted, and a repo nobody
// declared still refused - folding widens nothing, because a GitHub slug is unique
// only up to casing.
func TestPRRecordAcceptsCanonicalCasingOfOwnRepoAndUpstream(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://github.com/Atqamz/No-Mistakes.git", remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	writeFakeDispatch(t, dir, "gh", "", "$1 $2", `  "pr view") echo '{"state":"OPEN"}' ;;`)

	home := newHome(t)
	added := runHand(t, home, "project", "add", "https://github.com/Atqamz/No-Mistakes.git", "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	declared := runHand(t, home, "project", "upstream", "No-Mistakes", "KunchenGUID/No-Mistakes")
	if declared.code != 0 {
		t.Fatalf("project upstream: exit %d, stderr %q", declared.code, declared.stderr)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		if err := state.Write(home, state.Task{ID: id, Project: "No-Mistakes", Kind: state.KindShip, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ id, url string }{
		{"task-1", "https://github.com/atqamz/no-mistakes/pull/31"},
		{"task-2", "https://github.com/kunchenguid/no-mistakes/pull/597"},
	} {
		recorded := runHand(t, home, "pr", tc.id, tc.url)
		if recorded.code != 0 {
			t.Fatalf("pr %s %s: exit %d, stderr %q", tc.id, tc.url, recorded.code, recorded.stderr)
		}
		task, err := state.Read(home, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if task.PR != tc.url {
			t.Fatalf("task %s PR = %q, want %q", tc.id, task.PR, tc.url)
		}
	}

	foreign := runHand(t, home, "pr", "task-3", "https://github.com/someone/else/pull/1")
	assertInvocation(t, foreign, 3, "not project No-Mistakes's repo")
}

// TestGateOpenedUpstreamPRFoundWhenOriginRemoteCasingDiffers drives the dropped
// fork PR through the built binary: the upstream search keeps only PRs whose head
// repo is the project's own, and gh reports that head repo in GitHub's canonical
// casing while hand derives it from whatever casing the clone's origin remote was
// written in. Compared case-sensitively the landed PR is discarded, so an operator
// sees "PR: (none)" and teardown refuses work that is already merged.
func TestGateOpenedUpstreamPRFoundWhenOriginRemoteCasingDiffers(t *testing.T) {
	home, binaries := setupCasedGateProject(t, "https://github.com/Atqamz/No-Mistakes.git", "No-Mistakes")

	declared := runHand(t, home, "project", "upstream", "No-Mistakes", "KunchenGUID/No-Mistakes")
	if declared.code != 0 {
		t.Fatalf("project upstream: exit %d, stderr %q", declared.code, declared.stderr)
	}

	upstreamPR := "https://github.com/kunchenguid/no-mistakes/pull/597"
	writeFakeGHAnyCasing(t, binaries,
		[]string{"KunchenGUID/No-Mistakes", "kunchenguid/no-mistakes"},
		597, upstreamPR, "MERGED", "atqamz/no-mistakes")

	status := runHand(t, home, "status", "task-1")
	if status.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", status.code, status.stderr)
	}
	if !strings.Contains(status.stdout, upstreamPR) {
		t.Fatalf("status stdout = %q, want the merged upstream PR %s reported", status.stdout, upstreamPR)
	}

	tornDown := runHand(t, home, "teardown", "task-1")
	if tornDown.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", tornDown.code, tornDown.stderr)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want the landed task removed", exists, err)
	}
}

// TestGateOpenedPRLandsWhenUpstreamDeclaresOwnRepoInOtherCasing drives the other
// half: an upstream declared as the project's own repo in different casing is the
// same repo, so searching it as a second target returns the one PR twice and the
// same-tier rule refuses landed work as ambiguous.
func TestGateOpenedPRLandsWhenUpstreamDeclaresOwnRepoInOtherCasing(t *testing.T) {
	home, binaries := setupCasedGateProject(t, "https://github.com/atqamz/no-mistakes.git", "no-mistakes")

	declared := runHand(t, home, "project", "upstream", "no-mistakes", "ATQAMZ/No-Mistakes")
	if declared.code != 0 {
		t.Fatalf("project upstream: exit %d, stderr %q", declared.code, declared.stderr)
	}

	ownPR := "https://github.com/atqamz/no-mistakes/pull/9"
	writeFakeGHAnyCasing(t, binaries,
		[]string{"atqamz/no-mistakes", "ATQAMZ/No-Mistakes"},
		9, ownPR, "MERGED", "atqamz/no-mistakes")

	status := runHand(t, home, "status", "task-1")
	if status.code != 0 {
		t.Fatalf("status: exit %d, stderr %q", status.code, status.stderr)
	}
	if !strings.Contains(status.stdout, ownPR) {
		t.Fatalf("status stdout = %q, want the merged PR %s reported once, not refused as its own duplicate", status.stdout, ownPR)
	}

	tornDown := runHand(t, home, "teardown", "task-1")
	if tornDown.code != 0 {
		t.Fatalf("teardown: exit %d, stderr %q", tornDown.code, tornDown.stderr)
	}
	if exists, err := state.Exists(home, "task-1"); err != nil || exists {
		t.Fatalf("state.Exists after teardown = %v, %v, want the landed task removed", exists, err)
	}
}
