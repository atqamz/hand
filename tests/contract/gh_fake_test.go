//go:build contract

package contract

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

const (
	contractGHRepo       = "atqamz/hand"
	contractGHRepoCased  = "AtqaMZ/Hand"
	contractGHEdgeSHA    = "0123456789abcdef0123456789abcdef01234567"
	contractGHHeadRefOid = "898192c133d4786a9d5bee7b05fead923d5c902e"
)

func installContractGH(t *testing.T) {
	t.Helper()
	faketool.GH{
		PRs: []faketool.GHPR{{
			Number:     154,
			URL:        "https://github.com/atqamz/hand/pull/154",
			Branch:     "136-usage-limit-resume",
			State:      "MERGED",
			Repo:       contractGHRepo,
			HeadRepo:   contractGHRepo,
			HeadRefOid: contractGHHeadRefOid,
			Checks:     []string{"pass", "skipping"},
		}},
		Repos: []faketool.GHRepo{{
			Requested:     "atqamz/secondhand",
			NameWithOwner: contractGHRepo,
			URL:           "https://github.com/atqamz/hand",
		}},
		Responses: []faketool.GHResponse{{
			Command: "pr view",
			Stderr:  "Could not resolve to a PullRequest with the number of 999999.\n",
			Exit:    1,
		}},
	}.Install(t, faketool.Bin(t))
}

func TestGHFixturesPreservePullRequestAndRepositoryContracts(t *testing.T) {
	installContractGH(t)

	empty := run(t, "", "gh", "pr", "list", "--repo", contractGHRepo, "--head", "no-such-branch-contract-test",
		"--state", "all", "--limit", "200", "--json", "number,url,state,headRepository").requireCode(t, 0)
	if strings.TrimSpace(empty.stdout) != "[]" {
		t.Fatalf("stdout = %q, want an empty array rather than a failure", empty.stdout)
	}

	listed := run(t, "", "gh", "pr", "list", "--repo", contractGHRepoCased, "--head", "136-usage-limit-resume",
		"--state", "all", "--limit", "200", "--json", "number,url,state,headRepository").requireCode(t, 0)
	var prs []struct {
		State          string `json:"state"`
		HeadRepository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"headRepository"`
	}
	if err := json.Unmarshal([]byte(listed.stdout), &prs); err != nil {
		t.Fatalf("parse stdout %q: %v", listed.stdout, err)
	}
	if len(prs) != 1 || prs[0].State != "MERGED" || prs[0].HeadRepository.NameWithOwner != contractGHRepo {
		t.Fatalf("PRs = %+v, want one merged PR from %s", prs, contractGHRepo)
	}

	repo := run(t, "", "gh", "repo", "view", "atqamz/secondhand", "--json", "nameWithOwner,url").requireCode(t, 0)
	if strings.TrimSpace(repo.stdout) != `{"nameWithOwner":"atqamz/hand","url":"https://github.com/atqamz/hand"}` {
		t.Fatalf("repo = %q, want renamed canonical repository", repo.stdout)
	}
}

// installContractGH's canned "pr view" failure response answers every pr view call regardless of
// PR number, so headRefOid fixture coverage needs its own install without that override.
func TestGHFixturesPreserveHeadRefOid(t *testing.T) {
	faketool.GH{
		PRs: []faketool.GHPR{{
			Number:     154,
			URL:        "https://github.com/atqamz/hand/pull/154",
			Branch:     "136-usage-limit-resume",
			State:      "MERGED",
			Repo:       contractGHRepo,
			HeadRepo:   contractGHRepo,
			HeadRefOid: contractGHHeadRefOid,
		}},
	}.Install(t, faketool.Bin(t))

	head := run(t, "", "gh", "pr", "view", "154", "--repo", contractGHRepo, "--json", "headRefOid").requireCode(t, 0)
	var headBody struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(head.stdout), &headBody); err != nil {
		t.Fatalf("parse stdout %q: %v", head.stdout, err)
	}
	if headBody.HeadRefOid != contractGHHeadRefOid {
		t.Fatalf("headRefOid = %q, want %q", headBody.HeadRefOid, contractGHHeadRefOid)
	}
}

func TestGHFixturesPreserveFailuresAndCheckBuckets(t *testing.T) {
	installContractGH(t)

	run(t, "", "gh", "pr", "view", "999999", "--repo", contractGHRepo, "--json", "state").
		requireCode(t, 1).
		requireStderrContains(t, "Could not resolve to a PullRequest")

	checks := run(t, "", "gh", "pr", "checks", "154", "--repo", contractGHRepo, "--json", "bucket").requireCode(t, 0)
	var got []struct {
		Bucket string `json:"bucket"`
	}
	if err := json.Unmarshal([]byte(checks.stdout), &got); err != nil {
		t.Fatalf("parse stdout %q: %v", checks.stdout, err)
	}
	if len(got) != 2 || got[0].Bucket != "pass" || got[1].Bucket != "skipping" {
		t.Fatalf("buckets = %+v, want pass and skipping", got)
	}
}

func TestGHFixturesPreserveEdgeRefOutcomes(t *testing.T) {
	missing := faketool.GH{Responses: []faketool.GHResponse{
		{Command: "api repos/atqamz/hand/git/matching-refs/tags/edge", Stdout: "0\n"},
		{Command: "api repos/atqamz/hand/commits/edge", Stderr: "HTTP 404: Not Found\n", Exit: 1},
	}}
	missing.Install(t, faketool.Bin(t))

	refs := run(t, "", "gh", "api", "repos/atqamz/hand/git/matching-refs/tags/edge",
		"--jq", `[.[] | select(.ref == "refs/tags/edge")] | length`).requireCode(t, 0)
	if strings.TrimSpace(refs.stdout) != "0" {
		t.Fatalf("refs = %q, want zero matching edge refs", refs.stdout)
	}
	run(t, "", "gh", "api", "repos/atqamz/hand/commits/edge", "--jq", ".sha").
		requireCode(t, 1).
		requireStderrContains(t, "HTTP 404")

	published := faketool.GH{Responses: []faketool.GHResponse{
		{Command: "api repos/atqamz/hand/git/matching-refs/tags/edge", Stdout: "1\n"},
		{Command: "api repos/atqamz/hand/commits/edge", Stdout: contractGHEdgeSHA + "\n"},
	}}
	published.Install(t, faketool.Bin(t))

	refs = run(t, "", "gh", "api", "repos/atqamz/hand/git/matching-refs/tags/edge",
		"--jq", `[.[] | select(.ref == "refs/tags/edge")] | length`).requireCode(t, 0)
	if strings.TrimSpace(refs.stdout) != "1" {
		t.Fatalf("refs = %q, want one matching edge ref", refs.stdout)
	}
	commit := run(t, "", "gh", "api", "repos/atqamz/hand/commits/edge", "--jq", ".sha").requireCode(t, 0)
	if strings.TrimSpace(commit.stdout) != contractGHEdgeSHA {
		t.Fatalf("edge commit = %q, want %q", commit.stdout, contractGHEdgeSHA)
	}
}

// The absence-proving diagnostic is a contract, not a message: internal/ghutil reads absence off
// this exact shape and everything else as unknown, so a paraphrase in the fake would make every
// unit test's absence unreachable while the tests still passed.
func TestGHFixturesPreserveTheAbsentPullRequestDiagnostic(t *testing.T) {
	faketool.GH{PRs: []faketool.GHPR{{
		Number: 154,
		URL:    "https://github.com/atqamz/hand/pull/154",
		Branch: "136-usage-limit-resume",
		State:  "MERGED",
		Repo:   contractGHRepo,
	}}}.Install(t, faketool.Bin(t))

	for _, args := range [][]string{
		{"pr", "view", "999999", "--repo", contractGHRepo, "--json", "state"},
		{"pr", "view", "999999", "--repo", contractGHRepo, "--json", "headRefOid"},
		{"pr", "checks", "999999", "--repo", contractGHRepo, "--json", "bucket"},
	} {
		run(t, "", "gh", args...).
			requireCode(t, 1).
			requireStderrContains(t, "Could not resolve to a PullRequest with the number of 999999. (repository.pullRequest)")
	}
}

// A fake that never answers is how a caller's timeout and cancellation paths are reachable without a
// network: the process is killed by the caller's context, and nothing was observed.
func TestGHFixturesNeverAnswerAHungCommand(t *testing.T) {
	faketool.GH{Hang: []string{"pr view"}}.Install(t, faketool.Bin(t))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", "pr", "view", "154", "--json", "state")
	if err := c.Run(); err == nil {
		t.Fatal("gh answered a hung command, want it killed by the deadline")
	}
	if ctx.Err() == nil {
		t.Fatal("context outlived the call, want the deadline to be what ended it")
	}
}
