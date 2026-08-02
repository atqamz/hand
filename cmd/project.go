package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage the project registry",
	}
	cmd.AddCommand(newProjectAddCmd())
	cmd.AddCommand(newProjectListCmd())
	cmd.AddCommand(newProjectRemoveCmd())
	cmd.AddCommand(newProjectSyncCmd())
	return cmd
}

func newProjectAddCmd() *cobra.Command {
	var mode string
	var name string

	cmd := &cobra.Command{
		Use:   "add <repo-url>",
		Short: "Clone a git repository and register it",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := args[0]
			if err := validateProjectURL(url); err != nil {
				return err
			}
			if err := validateProjectMode(mode); err != nil {
				return err
			}
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if name == "" {
				name = project.DeriveName(url)
			}
			if err := validateProjectName(name); err != nil {
				return err
			}

			if _, exists, err := project.Find(home, name); err != nil {
				return err
			} else if exists {
				return &ExitError{Err: fmt.Errorf("project %q already registered", name), Code: 3}
			}

			clonePath := filepath.Join(home, "projects", name)
			if err := reserveCloneDestination(clonePath); err != nil {
				return err
			}
			if err := gitClone(url, clonePath); err != nil {
				if cleanupErr := os.RemoveAll(clonePath); cleanupErr != nil {
					return fmt.Errorf("%w; remove incomplete clone: %v", err, cleanupErr)
				}
				return err
			}

			if mode == project.ModeNoMistakes {
				if err := noMistakesInit(clonePath); err != nil {
					return cleanupCloneAfterFailure(clonePath, err)
				}
			}

			if err := treehouseInitIfNeeded(clonePath); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}

			if err := project.Add(home, project.Project{Name: name, URL: url, Mode: mode}); err != nil {
				return cleanupCloneAfterFailure(clonePath, err)
			}
			if err := dashboard.Update(home, dashboard.UpdateOpts{}); err != nil {
				_ = project.Remove(home, name)
				return cleanupCloneAfterFailure(clonePath, err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "added project %s (%s) mode=%s\n", name, url, mode); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&mode, "mode", project.ModeDirectPR, "delivery mode: no-mistakes, direct-pr, local-only")
	cmd.Flags().StringVar(&name, "name", "", "override the project name")
	return cmd
}

func cleanupCloneAfterFailure(clonePath string, cause error) error {
	if err := os.RemoveAll(clonePath); err != nil {
		return errors.Join(cause, fmt.Errorf("remove incomplete clone: %w", err))
	}
	return cause
}

func reserveCloneDestination(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create projects directory: %w", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return &ExitError{Err: fmt.Errorf("project destination %q already exists", path), Code: 3}
		}
		return fmt.Errorf("reserve project destination: %w", err)
	}
	return nil
}

func validateProjectName(name string) error {
	if !isRegistrySafeName(name) {
		return &ExitError{Err: fmt.Errorf("invalid project name %q: must be a registry-safe identifier", name), Code: 2}
	}
	return nil
}

func isRegistrySafeName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
		if i == 0 && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func validateProjectURL(url string) error {
	for _, prefix := range []string{"https://", "git@", "ssh://", "git://"} {
		if strings.HasPrefix(url, prefix) {
			return nil
		}
	}
	return &ExitError{Err: fmt.Errorf("invalid project URL %q: must start with https://, git@, ssh://, or git://", url), Code: 2}
}

func validateProjectMode(mode string) error {
	switch mode {
	case project.ModeNoMistakes, project.ModeDirectPR, project.ModeLocalOnly:
		return nil
	default:
		return &ExitError{Err: fmt.Errorf("invalid project mode %q: must be no-mistakes, direct-pr, or local-only", mode), Code: 2}
	}
}

func gitClone(url, dest string) error {
	c := exec.Command("git", "clone", url, dest)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}
	return nil
}

func noMistakesInit(clonePath string) error {
	c := exec.Command("no-mistakes", "init")
	c.Dir = clonePath
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("no-mistakes init failed: %s", string(out))
	}
	return nil
}

func treehouseInitIfNeeded(clonePath string) error {
	if _, err := os.Stat(filepath.Join(clonePath, "treehouse.toml")); err == nil {
		return nil
	}
	c := exec.Command("treehouse", "init")
	c.Dir = clonePath
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse init failed: %s", string(out))
	}
	return nil
}

func newProjectListCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			projects, err := project.List(home)
			if err != nil {
				return err
			}

			if asJSON {
				type projectJSON struct {
					Name      string `json:"name"`
					URL       string `json:"url"`
					Mode      string `json:"mode"`
					GateIssue string `json:"gate_issue,omitempty"`
				}
				out := make([]projectJSON, 0, len(projects))
				for _, p := range projects {
					out = append(out, projectJSON{Name: p.Name, URL: p.URL, Mode: p.Mode, GateIssue: gateIssue(home, p)})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, p := range projects {
				line := fmt.Sprintf("%s\t%s\t%s", p.Name, p.URL, p.Mode)
				if issue := gateIssue(home, p); issue != "" {
					line += "\t(gate: " + issue + ")"
				}
				if _, err := fmt.Fprintln(w, line); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

// gateIssue reports why a no-mistakes project's recorded mode cannot currently be honoured, so
// hand project list is the surface an operator catches a stale or missing gate registration on,
// instead of a worker discovering it mid-dispatch with nothing obliging it to say so.
func gateIssue(home string, p project.Project) string {
	if p.Mode != project.ModeNoMistakes {
		return ""
	}
	clonePath := filepath.Join(home, "projects", p.Name)
	gateState, err := project.GateStatus(clonePath)
	if err != nil {
		return "unreachable"
	}
	if gateState == project.GateNotInitialized {
		return "not initialized"
	}
	return ""
}

func newProjectRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a project",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if active, err := hasActiveTasksForProject(home, name); err != nil {
				return err
			} else if active {
				return &ExitError{Err: fmt.Errorf("project %q has active tasks referencing it", name), Code: 3}
			}

			if err := project.Remove(home, name); err != nil {
				return asPrecondition(err)
			}
			if err := dashboard.Update(home, dashboard.UpdateOpts{}); err != nil {
				return err
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "removed project %s (clone retained at projects/%s)\n", name, name); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

func hasActiveTasksForProject(home, name string) (bool, error) {
	tasks, err := state.List(home)
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if t.Project == name {
			return true, nil
		}
	}
	return false, nil
}

func newProjectSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Fast-forward project clones to their remote default branch",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			var targets []project.Project
			if len(args) == 1 {
				p, exists, err := project.Find(home, args[0])
				if err != nil {
					return err
				}
				if !exists {
					return &ExitError{Err: fmt.Errorf("project %q not registered", args[0]), Code: 3}
				}
				targets = []project.Project{p}
			} else {
				targets, err = project.List(home)
				if err != nil {
					return err
				}
			}

			advancedAny := false
			for _, p := range targets {
				releaseProject, err := state.Lock(home, "project:"+p.Name)
				if err != nil {
					return fmt.Errorf("lock project %q: %w", p.Name, err)
				}
				msg, advanced, syncErr := syncOneProject(home, p)
				releaseProject()

				if syncErr != nil {
					if len(targets) == 1 {
						return syncErr
					}
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", syncErr); err != nil {
						return err
					}
					continue
				}
				if advanced {
					advancedAny = true
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), msg); err != nil {
					return err
				}
			}

			if advancedAny {
				if err := dashboard.Update(home, dashboard.UpdateOpts{}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	return cmd
}

// syncOneProject fetches and, when eligible, fast-forwards a single project clone.
// It never errors on a benign skip (dirty, wrong branch, diverged, no remote) -
// those are reported in the returned message, per SPECS.md's fail-open policy.
func syncOneProject(home string, p project.Project) (string, bool, error) {
	clonePath := filepath.Join(home, "projects", p.Name)

	if !hasOriginRemote(clonePath) {
		return fmt.Sprintf("%s: skipped (no origin remote)", p.Name), false, nil
	}

	fetch := exec.Command("git", "fetch", "origin", "--prune")
	fetch.Dir = clonePath
	if out, err := fetch.CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("%s: git fetch failed: %s", p.Name, strings.TrimSpace(string(out)))
	}

	pruneGoneBranches(clonePath)

	defaultBr, err := defaultBranch(clonePath)
	if err != nil {
		return "", false, fmt.Errorf("%s: resolve default branch: %w", p.Name, err)
	}
	currentBr, err := currentBranch(clonePath)
	if err != nil {
		return "", false, fmt.Errorf("%s: current branch: %w", p.Name, err)
	}
	if currentBr != defaultBr {
		return fmt.Sprintf("%s: skipped (on branch %s, not %s)", p.Name, currentBr, defaultBr), false, nil
	}
	dirty, err := hasUncommittedChanges(clonePath)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", p.Name, err)
	}
	if dirty {
		return fmt.Sprintf("%s: skipped (dirty working tree)", p.Name), false, nil
	}

	remoteRef := "origin/" + defaultBr
	behind, err := commitCount(clonePath, "HEAD.."+remoteRef)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", p.Name, err)
	}
	if behind == 0 {
		return fmt.Sprintf("%s: up to date", p.Name), false, nil
	}
	ahead, err := commitCount(clonePath, remoteRef+"..HEAD")
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", p.Name, err)
	}
	if ahead > 0 {
		return fmt.Sprintf("%s: skipped (diverged from %s)", p.Name, remoteRef), false, nil
	}

	merge := exec.Command("git", "merge", "--ff-only", remoteRef)
	merge.Dir = clonePath
	if out, err := merge.CombinedOutput(); err != nil {
		return fmt.Sprintf("%s: skipped (fast-forward failed: %s)", p.Name, strings.TrimSpace(string(out))), false, nil
	}
	return fmt.Sprintf("%s: fast-forwarded to %s (was %d behind)", p.Name, remoteRef, behind), true, nil
}

func hasOriginRemote(clonePath string) bool {
	c := exec.Command("git", "remote", "get-url", "origin")
	c.Dir = clonePath
	return c.Run() == nil
}

func commitCount(clonePath, revRange string) (int, error) {
	c := exec.Command("git", "rev-list", "--count", revRange)
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s failed: %w", revRange, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count: %w", err)
	}
	return n, nil
}

// pruneGoneBranches best-effort deletes local branches whose upstream tracking
// branch is gone. Branches still checked out in a worktree refuse deletion;
// that failure is ignored since pruning must never block a sync.
func pruneGoneBranches(clonePath string) {
	c := exec.Command("git", "for-each-ref", "--format=%(refname:short)|%(upstream:track)", "refs/heads/")
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || !strings.Contains(parts[1], "[gone]") {
			continue
		}
		del := exec.Command("git", "branch", "-D", parts[0])
		del.Dir = clonePath
		_ = del.Run()
	}
}
