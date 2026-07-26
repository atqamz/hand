package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr <id> <url>",
		Short: "Record a task's pull request URL",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, url := args[0], args[1]
			if !state.ValidatePRURL(url) {
				return &ExitError{Err: fmt.Errorf("invalid PR URL %q: must match https://github.com/<owner>/<repo>/pull/<number>", url), Code: 2}
			}

			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			release, err := state.Lock(home, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			t, err := state.Read(home, id)
			if err != nil {
				return asPrecondition(err)
			}

			if t.PR == url {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "pr already recorded for %s: %s\n", t.ID, url)
				return err
			}
			if t.PR != "" {
				return &ExitError{Err: fmt.Errorf("task %s already has a different PR recorded: %s", t.ID, t.PR), Code: 3}
			}

			proj, exists, err := project.Find(home, t.Project)
			if err != nil {
				return err
			}
			if !exists {
				return &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
			}

			repoSlug, err := repoSlugForProject(home, proj)
			if err != nil {
				return err
			}
			urlSlug, ok := state.ParsePRURL(url)
			if !ok {
				return &ExitError{Err: fmt.Errorf("invalid PR URL %q", url), Code: 2}
			}
			if repoSlug != urlSlug {
				return &ExitError{Err: fmt.Errorf("PR %s belongs to %s, not project %s's repo (%s)", url, urlSlug, t.Project, repoSlug), Code: 3}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if _, err := ghutil.PRIsMerged(ctx, url); err != nil {
				return &ExitError{Err: fmt.Errorf("PR %s not found in %s: %w", url, repoSlug, err), Code: 3}
			}

			t.PR = url
			if err := state.Write(home, t); err != nil {
				return fmt.Errorf("write task state: %w", err)
			}

			dashPath := filepath.Join(home, "data", "dashboard.md")
			if err := dashboard.Update(dashPath, dashboard.UpdateOpts{SetPR: &dashboard.PRUpdate{ID: t.ID, PR: url}}); err != nil {
				return fmt.Errorf("update dashboard: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "recorded PR for %s: %s\n", t.ID, url)
			return err
		},
	}
	return cmd
}

// repoSlugForProject derives "owner/repo" from the project clone's own origin
// remote rather than the registry URL, so a PR is checked against the repo
// hand pr and gh actually operate on.
func repoSlugForProject(home string, p project.Project) (string, error) {
	clonePath := filepath.Join(home, "projects", p.Name)
	// config --get, not remote get-url: the latter resolves the URL through any
	// url.<base>.insteadOf rule (e.g. a corporate mirror or ssh-rewrite config)
	// before we ever see it, which could turn a genuine mismatch into a false
	// match or a false "can't derive repo" refusal. The raw stored value is
	// what hand pr and gh actually need to agree on.
	c := exec.Command("git", "config", "--get", "remote.origin.url")
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return "", &ExitError{Err: fmt.Errorf("resolve origin remote for project %q: %w", p.Name, err), Code: 3}
	}

	remote := strings.TrimSpace(string(out))
	slug, ok := ghutil.RepoSlugFromRemote(remote)
	if !ok {
		return "", &ExitError{Err: fmt.Errorf("cannot derive GitHub repo from origin remote %q for project %q", remote, p.Name), Code: 3}
	}
	return slug, nil
}
