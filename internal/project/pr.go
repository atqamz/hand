package project

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/state"
)

// ValidatePR is the single gate every PR URL passes before it is recorded on a task, from
// `hand pr` or from the watcher auto-recording one a worker embedded in a report line. A
// recorded PR feeds `gh pr merge` directly, so a foreign repo must never reach task state.
func ValidatePR(ctx context.Context, homeDir string, p Project, url string) error {
	repoSlug, err := RepoSlug(homeDir, p)
	if err != nil {
		return err
	}
	urlSlug, ok := state.ParsePRURL(url)
	if !ok {
		return fmt.Errorf("invalid PR URL %q", url)
	}
	// urlSlug is never empty here, so an undeclared upstream cannot match it. EqualFold, not
	// ==: a GitHub slug is unique only up to casing, so folding cannot admit a foreign repo,
	// while == refuses a landed PR whose canonical casing differs from either declared one.
	if !strings.EqualFold(urlSlug, repoSlug) && !strings.EqualFold(urlSlug, p.Upstream) {
		// A fork contribution opens its PR on the declared upstream rather than on the repo hand
		// pushes to, so p.Upstream passes too - but only because an operator declared it (hand
		// project upstream), never because the URL's repo looks related to the project's own.
		return fmt.Errorf("PR %s belongs to %s, not project %s's repo (%s)%s", url, urlSlug, p.Name, repoSlug, upstreamNote(p))
	}
	// Absence refuses because the PR is not there; an observation that did not complete refuses
	// too, but must never claim absence: an auth failure reporting "not found" would tell an
	// operator to fix a URL that was right all along.
	observation := ghutil.ObserveMergeState(ctx, url)
	if observation.Absent() {
		return fmt.Errorf("PR %s not found in %s", url, urlSlug)
	}
	if observation.Unknown() {
		return fmt.Errorf("PR %s in %s could not be observed, so nothing is recorded for it: %s", url, urlSlug, observation.Reason())
	}
	return nil
}

// Names the declared upstream in a refusal, and its absence in one for a project that has
// none: an operator whose fork contribution was refused has to tell "the upstream I declared
// is a different repo" from "this project declares no upstream at all", which is the remedy.
func upstreamNote(p Project) string {
	if p.Upstream == "" {
		return " and no upstream is declared for it"
	}
	return fmt.Sprintf(" or its declared upstream (%s)", p.Upstream)
}

// RepoSlug derives "owner/repo" from the project clone's own origin remote
// rather than the registry URL, so a PR is checked against the repo hand and gh
// actually operate on.
func RepoSlug(homeDir string, p Project) (string, error) {
	// config --get, not remote get-url: the latter resolves the URL through any
	// url.<base>.insteadOf rule (a corporate mirror, an ssh rewrite) first, which could turn a
	// genuine mismatch into a false match or refusal. hand and gh agree on the stored value.
	out, err := git.Run(filepath.Join(homeDir, "projects", p.Name), "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("resolve origin remote for project %q: %w", p.Name, err)
	}

	remote := strings.TrimSpace(string(out))
	slug, ok := ghutil.RepoSlugFromRemote(remote)
	if !ok {
		return "", fmt.Errorf("cannot derive GitHub repo from origin remote %q for project %q", remote, p.Name)
	}
	return slug, nil
}
