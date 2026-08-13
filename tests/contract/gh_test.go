//go:build contract

package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// This repo, and a PR of its own that is merged and will stay merged. Every gh
// call below is read-only: nothing in a contract run may open, close or merge a
// real PR, so `pr merge` is recorded in FIDELITY.md and left unexercised here.
const (
	ghRepo      = "atqamz/hand"
	ghRepoCased = "AtqaMZ/Hand"
	ghMergedPR  = "154"
	ghMergedRef = "136-usage-limit-resume"
)

func requireGH(t *testing.T) {
	t.Helper()
	requireBin(t, "gh")
	if res := run(t, "", "gh", "auth", "status"); res.code != 0 {
		t.Skipf("gh is not authenticated: %s", res.stderr)
	}
}

func TestGHPRListAnswersAnEmptyArrayForABranchWithNoPR(t *testing.T) {
	requireGH(t)

	res := run(t, "", "gh", "pr", "list", "--repo", ghRepo, "--head", "no-such-branch-contract-test",
		"--state", "all", "--limit", "200", "--json", "number,url,state,headRepository").requireCode(t, 0)
	if strings.TrimSpace(res.stdout) != "[]" {
		t.Fatalf("stdout = %q, want an empty array rather than a failure", res.stdout)
	}
}

// The slug case atqamz/hand#147 fixed: gh serves a repo under any casing and answers with
// the canonical one, so a comparison against a git remote has to fold case.
func TestGHServesARepoUnderAnyCasingAndAnswersCanonically(t *testing.T) {
	requireGH(t)

	res := run(t, "", "gh", "pr", "list", "--repo", ghRepoCased, "--head", ghMergedRef,
		"--state", "all", "--limit", "200", "--json", "number,url,state,headRepository").requireCode(t, 0)

	var prs []struct {
		Number         int    `json:"number"`
		State          string `json:"state"`
		HeadRepository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"headRepository"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &prs); err != nil {
		t.Fatalf("parse stdout %q: %v", res.stdout, err)
	}
	if len(prs) != 1 {
		t.Fatalf("got %d PRs for %s, want exactly the one", len(prs), ghMergedRef)
	}
	if prs[0].HeadRepository.NameWithOwner != ghRepo {
		t.Fatalf("headRepository = %q, want the canonical %q the query did not use",
			prs[0].HeadRepository.NameWithOwner, ghRepo)
	}
	if prs[0].State != "MERGED" {
		t.Fatalf("state = %q, want MERGED", prs[0].State)
	}
}

func TestGHRepoViewFollowsTheSecondhandRename(t *testing.T) {
	requireGH(t)

	res := run(t, "", "gh", "repo", "view", "atqamz/secondhand", "--json", "nameWithOwner,url").requireCode(t, 0)
	var repo struct {
		NameWithOwner string `json:"nameWithOwner"`
		URL           string `json:"url"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &repo); err != nil {
		t.Fatalf("parse stdout %q: %v", res.stdout, err)
	}
	if repo.NameWithOwner != "atqamz/hand" || repo.URL != "https://github.com/atqamz/hand" {
		t.Fatalf("repo = %+v, want canonical renamed repository", repo)
	}
}

func TestGHPRViewFailsForAPRThatDoesNotExist(t *testing.T) {
	requireGH(t)

	run(t, "", "gh", "pr", "view", ghMergedPR, "--repo", ghRepoCased, "--json", "state").requireCode(t, 0)
	run(t, "", "gh", "pr", "view", "999999", "--repo", ghRepo, "--json", "state").
		requireCode(t, 1).
		requireStderrContains(t, "Could not resolve to a PullRequest")
}

// prChecksGreen reads the buckets and ignores the exit code once the JSON
// parses, so what the fake owes is the bucket vocabulary, not the code.
func TestGHPRChecksReportsBuckets(t *testing.T) {
	requireGH(t)

	res := run(t, "", "gh", "pr", "checks", ghMergedPR, "--repo", ghRepo, "--json", "bucket")
	var checks []struct {
		Bucket string `json:"bucket"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &checks); err != nil {
		t.Fatalf("parse stdout %q: %v", res.stdout, err)
	}
	if len(checks) == 0 {
		t.Fatalf("no checks reported for PR %s, so the bucket shape is unverified", ghMergedPR)
	}
	known := map[string]bool{"pass": true, "fail": true, "pending": true, "skipping": true, "cancel": true}
	for _, check := range checks {
		if !known[check.Bucket] {
			t.Fatalf("bucket %q is outside the vocabulary the fake and prChecksGreen know", check.Bucket)
		}
	}
}

func TestGHRefusesACommitRefThatDoesNotExist(t *testing.T) {
	requireGH(t)

	res := run(t, "", "gh", "api", "repos/atqamz/hand/commits/no-such-edge-ref-contract-test", "--jq", ".sha")
	if res.code == 0 {
		t.Fatalf("exit 0 with stdout %q, want a refusal for an absent ref", res.stdout)
	}
	if strings.TrimSpace(res.stderr) == "" {
		t.Fatal("stderr is empty, want the diagnostic FIDELITY.md records for an absent ref")
	}
}

// Asserts in both worlds rather than skipping: matching-refs answers 200 with an
// empty array for a tag that is absent, so which branch is owed is knowable.
func TestGHEdgeRefReportsThePublishedCommit(t *testing.T) {
	requireGH(t)

	refs := run(t, "", "gh", "api", "repos/atqamz/hand/git/matching-refs/tags/edge",
		"--jq", `[.[] | select(.ref == "refs/tags/edge")] | length`).requireCode(t, 0)

	res := run(t, "", "gh", "api", "repos/atqamz/hand/commits/edge", "--jq", ".sha")
	if strings.TrimSpace(refs.stdout) == "0" {
		if res.code == 0 {
			t.Fatalf("commits/edge = %q at exit 0, want a refusal while no edge tag exists", res.stdout)
		}
		if strings.TrimSpace(res.stderr) == "" {
			t.Fatal("stderr is empty, want the diagnostic FIDELITY.md records for an absent ref")
		}
		return
	}
	res.requireCode(t, 0)
	sha := strings.TrimSpace(res.stdout)
	if len(sha) != 40 {
		t.Fatalf("edge commit = %q, want a full SHA", sha)
	}
}
