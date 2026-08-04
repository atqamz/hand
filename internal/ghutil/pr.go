// Package ghutil shells out to the gh CLI for PR access rather than calling the
// REST API directly; internal/selfupdate follows the same convention for
// GitHub Releases. Tests fake gh with a shell script on PATH (see writeFakeGHPRView
// in pr_test.go; the same pattern covers releases in
// internal/selfupdate/selfupdate_test.go and herdr in cmd/status_test.go).
package ghutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PRIsMerged reports whether the PR is merged. gh writes warnings to stderr ahead of
// the JSON, so the payload must be read from stdout alone; CombinedOutput here corrupts
// the parse (issue #21).
func PRIsMerged(ctx context.Context, pr string) (bool, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", pr, "--json", "state")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("gh pr view failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return false, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return body.State == "MERGED", nil
}

// PRSearchTarget names one repo FindPRByBranch searches for a head ref.
type PRSearchTarget struct {
	Repo string
	// When set, keeps only PRs whose head branch lives in that repo: a fork's upstream carries head
	// refs from every contributor, so a branch name alone can match a stranger's PR there. Folded,
	// because gh reports canonical casing while this comes from the clone's origin remote.
	HeadRepo string
}

// PRCandidate names one PR under consideration by FindPRByBranch, for use in
// an AmbiguousPRError message.
type PRCandidate struct {
	Repo   string
	Number int
	State  string
}

// AmbiguousPRError reports that a branch's PRs do not resolve to a usable winner: no preference
// tier yields a single match, or a merged PR coexists with an open one, which refuses by rule.
// Candidates names every PR on the head ref, any state and any searched repo, not just that tier.
type AmbiguousPRError struct {
	Branch     string
	Candidates []PRCandidate
}

func (e *AmbiguousPRError) Error() string {
	parts := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		parts[i] = fmt.Sprintf("%s#%d (%s)", c.Repo, c.Number, c.State)
	}
	return fmt.Sprintf("ambiguous PR for branch %s: %s", e.Branch, strings.Join(parts, ", "))
}

// FindPRByBranch reports the PR across targets whose head ref is exactly branch - the only rule
// hand uses to associate a PR with a task, never a title, an issue number or a task id. found is
// false when no PR carries that head ref.
func FindPRByBranch(ctx context.Context, branch string, targets ...PRSearchTarget) (url string, merged bool, found bool, err error) {
	// More than one target is how a fork project finds its PR: the branch is pushed to the fork
	// while the PR is opened on the declared upstream. Matches from every target resolve through
	// one tier pass, so a fork PR and an upstream PR are ambiguous exactly like two in one repo.
	var results []prListItem
	for _, target := range targets {
		found, err := listPRsByBranch(ctx, target, branch)
		if err != nil {
			return "", false, false, err
		}
		results = append(results, found...)
	}
	if len(results) == 0 {
		return "", false, false, nil
	}

	// A branch can carry more than one PR - a closed-unmerged one plus a reopened replacement - so
	// results resolve by preference tier rather than arbitrarily: merged, then open, then
	// closed-unmerged, and a tier holding more than one match is ambiguous.
	var mergedPRs, openPRs, closedPRs []prListItem
	for _, r := range results {
		switch r.State {
		case "MERGED":
			mergedPRs = append(mergedPRs, r)
		case "OPEN":
			openPRs = append(openPRs, r)
		case "CLOSED":
			closedPRs = append(closedPRs, r)
		}
	}

	// A merged PR coexisting with an open one refuses too: the open PR is live evidence the branch
	// may still carry unlanded work. Guessing here is what let cmd/teardown.go's landed-work guard
	// trust a merged PR while the branch's real state was closed-unmerged (atqamz/secondhand#77).
	if len(mergedPRs) > 0 && len(openPRs) > 0 {
		return "", false, false, ambiguousPRError(branch, results)
	}

	for _, matches := range [][]prListItem{mergedPRs, openPRs, closedPRs} {
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0].URL, matches[0].State == "MERGED", true, nil
		default:
			return "", false, false, ambiguousPRError(branch, results)
		}
	}
	return "", false, false, fmt.Errorf("gh pr list returned no PR in a recognized state for branch %s", branch)
}

func listPRsByBranch(ctx context.Context, target PRSearchTarget, branch string) ([]prListItem, error) {
	// --state all because gh pr list defaults to open only and a gate-opened PR may already be
	// merged or closed; --limit stated rather than left on gh's implicit 30, far above any real
	// count for one branch, so a same-tier duplicate cannot be truncated into a lone winner.
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--repo", target.Repo, "--head", branch, "--state", "all", "--limit", "200", "--json", "number,url,state,headRepository")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var results []prListItem
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}
	// gh's --head takes the plain branch name even for a cross-repo PR (the qualified owner:branch
	// form matches nothing), so the upstream target carries a HeadRepo to keep a same-named branch
	// from another fork out.
	kept := make([]prListItem, 0, len(results))
	for _, r := range results {
		if target.HeadRepo != "" && !strings.EqualFold(r.HeadRepository.NameWithOwner, target.HeadRepo) {
			continue
		}
		r.Repo = target.Repo
		kept = append(kept, r)
	}
	return kept, nil
}

func ambiguousPRError(branch string, matches []prListItem) *AmbiguousPRError {
	candidates := make([]PRCandidate, len(matches))
	for i, m := range matches {
		candidates[i] = PRCandidate{Repo: m.Repo, Number: m.Number, State: m.State}
	}
	return &AmbiguousPRError{Branch: branch, Candidates: candidates}
}

// One entry of `gh pr list --json number,url,state,headRepository`. Repo is the searched repo, not
// part of gh's payload: a match's own repo has to survive into an AmbiguousPRError naming PRs from
// two repos at once.
type prListItem struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	State          string `json:"state"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	Repo string `json:"-"`
}

// RepoSlugFromRemote extracts "owner/repo" from a GitHub origin remote URL in
// https, ssh, or git@ form. Returns ok=false on anything else, so a caller
// like hand pr can refuse rather than guess which repo a PR belongs to.
func RepoSlugFromRemote(remoteURL string) (string, bool) {
	s := strings.TrimSuffix(remoteURL, ".git")
	switch {
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		s = strings.TrimPrefix(s, "ssh://git@github.com/")
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	default:
		return "", false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}
