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

// PRCandidate names one PR under consideration by FindPRByBranch, for use in
// an AmbiguousPRError message.
type PRCandidate struct {
	Number int
	State  string
}

// AmbiguousPRError reports that a branch's PRs do not resolve to a single
// winner at any preference tier (two merged, two open, ...). FindPRByBranch
// returns this instead of guessing; the caller decides.
type AmbiguousPRError struct {
	Branch     string
	Candidates []PRCandidate
}

func (e *AmbiguousPRError) Error() string {
	parts := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		parts[i] = fmt.Sprintf("#%d (%s)", c.Number, c.State)
	}
	return fmt.Sprintf("ambiguous PR for branch %s: %s", e.Branch, strings.Join(parts, ", "))
}

// FindPRByBranch reports the PR on repoSlug whose head ref is exactly branch -
// the only rule hand uses to associate a PR with a task, never a title, issue
// number or task id. --state all is required because gh pr list defaults to
// open only, and a gate-opened PR may already be merged or closed by the time
// hand looks for it; found is false when no PR has that head ref.
//
// A branch can carry more than one PR (a closed-unmerged one plus a reopened
// replacement, say), so results are resolved by preference tier rather than
// picked arbitrarily: merged wins over open, open wins over closed-unmerged.
// A tier with more than one match is ambiguous and returns AmbiguousPRError
// naming every candidate, rather than guessing (atqamz/secondhand#77) - the
// same guess that let cmd/teardown.go's landed-work guard trust a merged PR
// while the branch's real state was closed-unmerged. That rule is only sound
// on the complete set of PRs for the head ref, so --limit is stated explicitly
// rather than left on gh pr list's implicit 30: the cap is set far above any
// realistic count for one branch, so a same-tier duplicate cannot be truncated
// out of the page and silently resolve as a single winner.
func FindPRByBranch(ctx context.Context, repoSlug, branch string) (url string, merged bool, found bool, err error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--repo", repoSlug, "--head", branch, "--state", "all", "--limit", "200", "--json", "number,url,state")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", false, false, fmt.Errorf("gh pr list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var results []prListItem
	if err := json.Unmarshal(out, &results); err != nil {
		return "", false, false, fmt.Errorf("parse gh pr list output: %w", err)
	}
	if len(results) == 0 {
		return "", false, false, nil
	}

	for _, tier := range []string{"MERGED", "OPEN", "CLOSED"} {
		var matches []prListItem
		for _, r := range results {
			if r.State == tier {
				matches = append(matches, r)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0].URL, matches[0].State == "MERGED", true, nil
		default:
			candidates := make([]PRCandidate, len(matches))
			for i, m := range matches {
				candidates[i] = PRCandidate{Number: m.Number, State: m.State}
			}
			return "", false, false, &AmbiguousPRError{Branch: branch, Candidates: candidates}
		}
	}
	return "", false, false, fmt.Errorf("gh pr list returned no PR in a recognized state for branch %s", branch)
}

// prListItem is one entry of `gh pr list --json number,url,state`.
type prListItem struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
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
