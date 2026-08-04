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
	ghRepo      = "atqamz/secondhand"
	ghRepoCased = "AtqaMZ/SecondHand"
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

// The slug case #147 fixed: gh serves a repo under any casing and answers with
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
