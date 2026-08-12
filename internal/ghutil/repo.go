package ghutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// The repository identity gh reports after resolving a requested slug.
type CanonicalRepo struct {
	NameWithOwner string
	URL           string
}

// Resolves a repository slug through gh and returns its canonical identity.
func ResolveCanonicalRepo(ctx context.Context, slug string) (CanonicalRepo, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", slug, "--json", "nameWithOwner,url")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return CanonicalRepo{}, fmt.Errorf("gh repo view failed for %s: %w: %s", slug, err, strings.TrimSpace(stderr.String()))
	}
	var repo CanonicalRepo
	if err := json.Unmarshal(out, &repo); err != nil {
		return CanonicalRepo{}, fmt.Errorf("parse gh repo view output: %w", err)
	}
	repo.NameWithOwner = strings.TrimSpace(repo.NameWithOwner)
	repo.URL = strings.TrimSpace(repo.URL)
	if repo.NameWithOwner == "" || repo.URL == "" {
		return CanonicalRepo{}, fmt.Errorf("gh repo view output missing nameWithOwner or url")
	}
	return repo, nil
}

// Rewrites a recognized GitHub remote to a validated canonical slug while preserving its transport and suffix.
func RewriteGitHubRemote(remoteURL, newSlug string) (string, bool) {
	if _, ok := RepoSlugFromRemote(remoteURL); !ok || !validRepoSlug(newSlug) {
		return "", false
	}
	suffix := ""
	if strings.HasSuffix(remoteURL, ".git") {
		suffix = ".git"
	}
	var prefix string
	switch {
	case strings.HasPrefix(remoteURL, "https://github.com/"):
		prefix = "https://github.com/"
	case strings.HasPrefix(remoteURL, "ssh://git@github.com/"):
		prefix = "ssh://git@github.com/"
	case strings.HasPrefix(remoteURL, "git@github.com:"):
		prefix = "git@github.com:"
	default:
		return "", false
	}
	return prefix + newSlug + suffix, true
}

func validRepoSlug(slug string) bool {
	parsed, ok := RepoSlugFromRemote("https://github.com/" + slug)
	return ok && parsed == slug
}
