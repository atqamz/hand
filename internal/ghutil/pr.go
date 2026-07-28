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

// FindPRByBranch reports the PR on repoSlug whose head ref is exactly branch -
// the only rule hand uses to associate a PR with a task, never a title, issue
// number or task id. --state all is required because gh pr list defaults to
// open only, and a gate-opened PR may already be merged or closed by the time
// hand looks for it; found is false when no PR has that head ref.
func FindPRByBranch(ctx context.Context, repoSlug, branch string) (url string, merged bool, found bool, err error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--repo", repoSlug, "--head", branch, "--state", "all", "--json", "url,state", "--limit", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", false, false, fmt.Errorf("gh pr list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var results []struct {
		URL   string `json:"url"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		return "", false, false, fmt.Errorf("parse gh pr list output: %w", err)
	}
	if len(results) == 0 {
		return "", false, false, nil
	}
	return results[0].URL, results[0].State == "MERGED", true, nil
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
