package project

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/state"
)

// ValidatePR is the single gate every PR URL passes before it is recorded on a
// task, whether it came from `hand pr` or from the watcher auto-recording a URL
// a worker embedded in a report line. A recorded PR feeds `gh pr merge`
// directly, so a URL naming a foreign repo must never reach task state.
//
// A fork contribution opens its PR on the project's declared upstream rather
// than on the repo hand pushes to, so that repo passes too - but only because
// an operator declared it (hand project upstream), never because the URL's repo
// happens to look related to the project's own.
func ValidatePR(ctx context.Context, homeDir string, p Project, url string) error {
	repoSlug, err := RepoSlug(homeDir, p)
	if err != nil {
		return err
	}
	urlSlug, ok := state.ParsePRURL(url)
	if !ok {
		return fmt.Errorf("invalid PR URL %q", url)
	}
	// urlSlug is never empty here, so an undeclared upstream cannot match it.
	if urlSlug != repoSlug && urlSlug != p.Upstream {
		return fmt.Errorf("PR %s belongs to %s, not project %s's repo (%s)%s", url, urlSlug, p.Name, repoSlug, upstreamNote(p))
	}
	if _, err := ghutil.PRIsMerged(ctx, url); err != nil {
		return fmt.Errorf("PR %s not found in %s: %w", url, urlSlug, err)
	}
	return nil
}

// upstreamNote names the declared upstream in a refusal, and its absence in
// one for a project that has none: an operator whose fork contribution was
// refused has to be able to tell "the upstream I declared is a different repo"
// from "this project declares no upstream at all", which is the remedy.
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
	// url.<base>.insteadOf rule (e.g. a corporate mirror or ssh-rewrite config)
	// before we ever see it, which could turn a genuine mismatch into a false
	// match or a false "can't derive repo" refusal. The raw stored value is
	// what hand and gh actually need to agree on.
	c := exec.Command("git", "config", "--get", "remote.origin.url")
	c.Dir = filepath.Join(homeDir, "projects", p.Name)
	out, err := c.Output()
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
