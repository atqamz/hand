package faketool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGH(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	c := exec.Command("gh", args...)
	var out, errOut strings.Builder
	c.Stdout = &out
	c.Stderr = &errOut
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("run gh %v: %v", args, err)
	}
	return out.String(), errOut.String(), code
}

func onePR() GH {
	return GH{PRs: []GHPR{{
		Number: 9,
		URL:    "https://github.com/owner/repo/pull/9",
		Branch: "task-1-branch",
		Repo:   "owner/repo",
	}}}
}

// The state-change every gh fake in the tree used to answer identically: after a
// merge, `pr view` has to say MERGED, or nothing can tell a merged PR from an open
// one and teardown's landed-work check is never really exercised.
func TestGHPRViewReportsMergedAfterAMerge(t *testing.T) {
	onePR().Install(t, Bin(t))

	before, _, code := runGH(t, "pr", "view", "9", "--json", "state")
	if code != 0 || !strings.Contains(before, `"state":"OPEN"`) {
		t.Fatalf("pr view = %q (exit %d), want OPEN", before, code)
	}
	if _, errOut, code := runGH(t, "pr", "merge", "9", "--squash"); code != 0 {
		t.Fatalf("pr merge exit %d (%q), want 0", code, errOut)
	}
	after, _, code := runGH(t, "pr", "view", "9", "--json", "state")
	if code != 0 || !strings.Contains(after, `"state":"MERGED"`) {
		t.Fatalf("pr view = %q (exit %d), want MERGED after the merge", after, code)
	}
	list, _, _ := runGH(t, "pr", "list", "--head", "task-1-branch", "--json", "number,url,state,headRepository")
	if !strings.Contains(list, `"state":"MERGED"`) {
		t.Fatalf("pr list = %q, want the listing to agree with pr view", list)
	}
}

// Recorded from gh 2.97.0 against an already-merged PR: a repeated merge is exit 0
// with a warning on stderr, so runPRMerge's exit-code check reads it as success.
func TestGHMergingAnAlreadyMergedPRIsExitZeroWithAWarning(t *testing.T) {
	g := onePR()
	g.PRs[0].State = "MERGED"
	g.Install(t, Bin(t))

	stdout, errOut, code := runGH(t, "pr", "merge", "9", "--squash")
	if code != 0 {
		t.Fatalf("pr merge exit %d, want 0 for an already-merged PR", code)
	}
	if !strings.Contains(errOut, "already merged") {
		t.Fatalf("stderr = %q, want the already-merged warning", errOut)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want the warning on stderr alone", stdout)
	}
}

// A URL and a number name the same PR, so a merge through one is visible through
// the other - which is what teardown and merge actually do, one each.
func TestGHResolvesAPRByNumberAndByURL(t *testing.T) {
	onePR().Install(t, Bin(t))

	if _, _, code := runGH(t, "pr", "merge", "https://github.com/owner/repo/pull/9", "--merge"); code != 0 {
		t.Fatal("pr merge by url failed")
	}
	out, _, _ := runGH(t, "pr", "view", "9", "--json", "state")
	if !strings.Contains(out, `"state":"MERGED"`) {
		t.Fatalf("pr view by number = %q, want the merge made through the url", out)
	}
}

func TestGHPRListAnswersAnEmptyArrayForABranchWithNoPR(t *testing.T) {
	onePR().Install(t, Bin(t))

	out, errOut, code := runGH(t, "pr", "list", "--head", "no-such-branch", "--json", "number,url,state,headRepository")
	if code != 0 {
		t.Fatalf("pr list exit %d (%q), want 0: no PR is not a failure", code, errOut)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("pr list = %q, want an empty array", out)
	}
}

func TestGHPRListReportsEveryPROnTheBranch(t *testing.T) {
	GH{PRs: []GHPR{
		{Number: 7, URL: "https://github.com/owner/repo/pull/7", Branch: "task-1-branch", State: "CLOSED", Repo: "owner/repo"},
		{Number: 9, URL: "https://github.com/owner/repo/pull/9", Branch: "task-1-branch", Repo: "owner/repo"},
	}}.Install(t, Bin(t))

	out, _, code := runGH(t, "pr", "list", "--head", "task-1-branch", "--json", "number,url,state,headRepository")
	if code != 0 {
		t.Fatalf("pr list exit %d, want 0", code)
	}
	for _, want := range []string{`"number":7`, `"state":"CLOSED"`, `"number":9`, `"state":"OPEN"`, `"nameWithOwner":"owner/repo"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("pr list = %q, want %s", out, want)
		}
	}
}

// A fork project searches its own repo and its declared upstream for the same
// branch, so each search has to see only its own repo's PRs, and in whatever
// casing the caller wrote the slug in.
func TestGHPRListNarrowsToTheRepoAskedForInAnyCasing(t *testing.T) {
	GH{PRs: []GHPR{
		{Number: 3, URL: "https://github.com/owner/fork/pull/3", Branch: "task-1-branch", Repo: "owner/fork"},
		{Number: 7, URL: "https://github.com/up/repo/pull/7", Branch: "task-1-branch", Repo: "up/repo", HeadRepo: "owner/fork"},
	}}.Install(t, Bin(t))

	for _, repo := range []string{"up/repo", "Up/Repo", "UP/REPO"} {
		out, _, code := runGH(t, "pr", "list", "--repo", repo, "--head", "task-1-branch", "--json", "number,url,state,headRepository")
		if code != 0 {
			t.Fatalf("pr list --repo %s exit %d, want 0", repo, code)
		}
		if !strings.Contains(out, `"number":7`) || strings.Contains(out, `"number":3`) {
			t.Fatalf("pr list --repo %s = %q, want the upstream PR alone", repo, out)
		}
		if !strings.Contains(out, `"nameWithOwner":"owner/fork"`) {
			t.Fatalf("pr list --repo %s = %q, want the head repo reported as the fork", repo, out)
		}
	}

	out, _, _ := runGH(t, "pr", "list", "--repo", "other/repo", "--head", "task-1-branch", "--json", "number,url,state,headRepository")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("pr list for an unrelated repo = %q, want an empty array", out)
	}
}

func TestGHPRChecksReportsTheDeclaredBuckets(t *testing.T) {
	g := onePR()
	g.PRs[0].Checks = []string{"pass", "fail"}
	g.Install(t, Bin(t))

	out, _, code := runGH(t, "pr", "checks", "9", "--json", "bucket")
	if code != 0 || out != "[{\"bucket\":\"pass\"},{\"bucket\":\"fail\"}]\n" {
		t.Fatalf("pr checks = %q (exit %d), want the declared buckets", out, code)
	}
}

func TestGHRepoViewReturnsCanonicalRepository(t *testing.T) {
	log := filepath.Join(t.TempDir(), "gh.log")
	GH{Repos: []GHRepo{{
		Requested:     "atqamz/secondhand",
		NameWithOwner: "atqamz/hand",
		URL:           "https://github.com/atqamz/hand",
	}}, Log: log}.Install(t, Bin(t))

	out, errOut, code := runGH(t, "repo", "view", "atqamz/secondhand", "--json", "nameWithOwner,url")
	if code != 0 || errOut != "" {
		t.Fatalf("gh repo view = %q, %q, exit %d, want JSON and exit 0", out, errOut, code)
	}
	want := `{"nameWithOwner":"atqamz/hand","url":"https://github.com/atqamz/hand"}` + "\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	logData, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "gh repo view atqamz/secondhand --json nameWithOwner,url") {
		t.Fatalf("log = %q, want canonical lookup invocation", logData)
	}
}

func TestGHRepoViewRefusesUnknownRepository(t *testing.T) {
	GH{}.Install(t, Bin(t))

	_, errOut, code := runGH(t, "repo", "view", "owner/missing", "--json", "nameWithOwner,url")
	if code == 0 || !strings.Contains(errOut, "repository not found") {
		t.Fatalf("gh repo view = %q, exit %d, want repository failure", errOut, code)
	}
}

func TestGHRefusesAPRItDoesNotKnow(t *testing.T) {
	onePR().Install(t, Bin(t))

	for _, args := range [][]string{
		{"pr", "view", "404", "--json", "state"},
		{"pr", "merge", "404", "--squash"},
		{"pr", "checks", "404", "--json", "bucket"},
	} {
		_, errOut, code := runGH(t, args...)
		if code == 0 || !strings.Contains(errOut, "no such pull request") {
			t.Fatalf("%v = %q (exit %d), want a refusal", args, errOut, code)
		}
	}
}

func TestGHReleaseAndEdgeRefExposeDeclaredState(t *testing.T) {
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, "checksums.txt"), []byte("checksums"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := "0123456789abcdef0123456789abcdef01234567"
	GH{Releases: []GHRelease{
		{StableTag: "v0.5.0", EdgeCommit: commit, EdgeNotes: "edge notes", EdgeAssetsDir: assets},
	}}.Install(t, Bin(t))

	stable, stderr, code := runGH(t, "release", "view", "--repo", "atqamz/hand", "--json", "tagName", "--jq", ".tagName")
	if code != 0 || stderr != "" || stable != "v0.5.0" {
		t.Fatalf("stable release = %q, %q, exit %d, want v0.5.0", stable, stderr, code)
	}
	edge, stderr, code := runGH(t, "api", "repos/atqamz/hand/commits/edge", "--jq", ".sha")
	if code != 0 || stderr != "" || edge != commit {
		t.Fatalf("edge ref = %q, %q, exit %d, want %s", edge, stderr, code, commit)
	}

	downloaded := t.TempDir()
	_, stderr, code = runGH(t, "release", "download", "edge", "--repo", "atqamz/hand", "--dir", downloaded, "--pattern", "checksums.txt")
	if code != 0 || stderr != "" {
		t.Fatalf("edge download = %q, exit %d, want success", stderr, code)
	}
	if _, err := os.Stat(filepath.Join(downloaded, "checksums.txt")); err != nil {
		t.Fatalf("downloaded checksums: %v", err)
	}
}
