package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/secondhand/internal/dashboard"
	"github.com/atqamz/secondhand/internal/ghutil"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/atqamz/secondhand/internal/worktree"
	"github.com/spf13/cobra"
)

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Clean up a completed task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
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

			dirtWasSafe := false
			if !force {
				updated, safeDirt, err := checkLandedWork(cmd.Context(), home, t)
				if err != nil {
					return err
				}
				t = updated
				dirtWasSafe = safeDirt
			}
			releaseProject, err := state.Lock(home, "project:"+t.Project)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", t.Project, err)
			}
			defer releaseProject()

			client := herdr.NewClient()
			if err := closeTaskTab(client, t.Herdr.WorkspaceID, t.Herdr.TabID); err != nil {
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: herdr tab close failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			// treehouse refuses to clean a dirty worktree without --force, and
			// nothing is left to answer its prompt here, so dirt this command
			// already judged discardable has to be returned forcibly or the
			// slot goes back to the pool still dirty.
			if err := worktree.Return(t.Worktree, force || dirtWasSafe); err != nil {
				return err
			}

			if err := state.Delete(home, id); err != nil {
				return asPrecondition(err)
			}

			completion := completionFor(t, force)
			dashPath := filepath.Join(home, "data", "dashboard.md")
			if err := dashboard.Update(dashPath, dashboard.UpdateOpts{Complete: &completion}); err != nil {
				return fmt.Errorf("update dashboard: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "teardown %s complete\n", id); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip landed-work checks")
	return cmd
}

func completionFor(t state.Task, forced bool) dashboard.Completion {
	c := dashboard.Completion{ID: t.ID, Project: t.Project, Kind: t.Kind}
	switch {
	case forced:
		c.Outcome = "torn-down"
		c.Detail = "forced (landed-work checks skipped)"
	case t.Kind == state.KindScout:
		c.Outcome = "done"
		c.Detail = "report " + filepath.Join("data", t.ID, "report.md")
	case t.PR != "":
		c.Outcome = "merged"
		c.Detail = "PR " + t.PR
	default:
		c.Outcome = "merged"
		c.Detail = "branch merged"
	}
	return c
}

// checkLandedWork reports whether the task's work is landed, and whether it got
// there past dirt it judged safe to discard - the caller has to force the
// worktree return in that case, since treehouse will not clean a dirty worktree
// on its own.
func checkLandedWork(ctx context.Context, home string, t state.Task) (state.Task, bool, error) {
	if t.Kind == state.KindScout {
		reportPath := filepath.Join("data", t.ID, "report.md")
		if _, err := os.Stat(filepath.Join(home, reportPath)); err != nil {
			return t, false, &ExitError{Err: fmt.Errorf("report not found at %s", reportPath), Code: 3}
		}
		return t, false, nil
	}

	status, err := gitStatusPorcelain(t.Worktree)
	if err != nil {
		return t, false, err
	}
	dirtWasSafe := false
	if status != "" {
		if !dirtIsSafeToDiscard(t.Worktree, status) {
			return t, false, &ExitError{Err: fmt.Errorf("uncommitted changes in worktree %s:\n%s", t.Worktree, capStatusLines(status)), Code: 3}
		}
		dirtWasSafe = true
	}

	if t.PR == "" {
		proj, exists, err := project.Find(home, t.Project)
		if err != nil {
			return t, false, err
		}
		if exists && proj.Mode == project.ModeLocalOnly {
			merged, err := branchIsMerged(filepath.Join(home, "projects", t.Project), t.Worktree)
			if err != nil {
				return t, false, err
			}
			if !merged {
				return t, false, &ExitError{Err: fmt.Errorf("branch for %s is not merged into the default branch", t.ID), Code: 3}
			}
			return t, dirtWasSafe, nil
		}

		// A gate-opened PR bypasses hand pr entirely (issue #69), so t.PR can still
		// be empty for landed work; detect it here rather than only refusing on it,
		// so the merged check below reads the same PR state hand pr would have
		// recorded. Detection failing (no clone on disk yet, gh unreachable, ...)
		// is not this command's failure to report: it falls through to the same
		// "no PR recorded" refusal below that a project with no detection at all
		// would have gotten.
		//
		// An ambiguous branch is a different failure and must not fall through the
		// same way: "no PR recorded" reads as unlanded, but ambiguous means unknown,
		// and picking either meaning here for the operator is the guess atqamz/secondhand#77
		// exists to remove.
		if exists {
			detected, err := detectPR(ctx, home, t, proj)
			var ambiguous *ghutil.AmbiguousPRError
			if errors.As(err, &ambiguous) {
				return t, false, &ExitError{Err: fmt.Errorf("PR for %s is ambiguous, refusing to guess: %w", t.ID, ambiguous), Code: 3}
			}
			if err == nil {
				t = detected
			}
		}

		if t.PR == "" {
			return t, false, &ExitError{Err: fmt.Errorf("no PR recorded for %s and project is not local-only: work may not be landed", t.ID), Code: 3}
		}
	}

	merged, err := ghutil.PRIsMerged(ctx, t.PR)
	if err != nil {
		return t, false, err
	}
	if !merged {
		return t, false, &ExitError{Err: fmt.Errorf("PR %s is not merged", t.PR), Code: 3}
	}
	return t, dirtWasSafe, nil
}

func gitStatusPorcelain(worktreePath string) (string, error) {
	c := exec.Command("git", "status", "--porcelain")
	c.Dir = worktreePath
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("git status failed: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func hasUncommittedChanges(worktreePath string) (bool, error) {
	status, err := gitStatusPorcelain(worktreePath)
	if err != nil {
		return false, err
	}
	return status != "", nil
}

// maxDirtStatusLines bounds the refusal's git status dump (atqamz/secondhand#65
// is the same lesson for report rendering): an unbounded dump into a session is its
// own problem, so this prints the first N entries and a count of the rest.
const maxDirtStatusLines = 20

func capStatusLines(status string) string {
	trimmed := strings.TrimRight(status, "\n")
	if trimmed == "" {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxDirtStatusLines {
		return strings.Join(lines, "\n")
	}
	rest := len(lines) - maxDirtStatusLines
	return strings.Join(lines[:maxDirtStatusLines], "\n") + fmt.Sprintf("\n...and %d more", rest)
}

// dirtIsSafeToDiscard reports whether every uncommitted change in status - the
// worktree's `git status --porcelain` output - is a tracked modification whose
// content already matches the local default branch's tip byte-for-byte: the
// no-mistakes gate re-editing a file to exactly the content its own merged fix
// already carries (atqamz/secondhand#79). Discarding dirt like that on teardown
// loses nothing.
//
// Each porcelain line reports two layers, index and working tree, and a change at
// either one is content that teardown would throw away, so each layer that reports
// a change is compared against the base on its own: an "MM" path whose working copy
// matches the base still hides a third, differing version staged in the index.
//
// Untracked files are never safe: there is nothing in the base to compare them
// against, so their mere presence fails this check. Checking only that the path
// exists in the base, or comparing paths without comparing content, both pass
// cases that lose data (a same-named file with different content, or a path
// that only coincidentally matches); this compares actual bytes for that reason.
//
// Every failure to resolve, read, or parse fails closed - the caller gets the
// ordinary refusal, never a discard on unverified dirt. Resolution is local-only,
// no fetch: a stale local ref just means a real safe case is missed.
func dirtIsSafeToDiscard(worktreePath, status string) bool {
	if status == "" {
		return true
	}
	baseRef, err := localDefaultBranchRef(worktreePath)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			return false
		}
		indexState, workingState, path := line[0], line[1], line[3:]
		if indexState == '?' || workingState == '?' {
			return false
		}
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+len(" -> "):]
		}

		base, err := gitShowBlob(worktreePath, baseRef, path)
		if err != nil {
			return false
		}
		if indexState != ' ' {
			staged, err := gitShowBlob(worktreePath, "", path)
			if err != nil || !bytes.Equal(staged, base) {
				return false
			}
		}
		if workingState != ' ' {
			working, err := os.ReadFile(filepath.Join(worktreePath, path))
			if err != nil || !bytes.Equal(working, base) {
				return false
			}
		}
	}
	return true
}

// localDefaultBranchRef resolves the worktree's local knowledge of the default
// branch without touching the network: a real treehouse worktree shares its
// refs with the project clone it was leased from, so a prior fetch there
// already left refs/remotes/origin/HEAD in place for it to read directly.
func localDefaultBranchRef(worktreePath string) (string, error) {
	c := exec.Command("git", "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD")
	c.Dir = worktreePath
	if out, err := c.Output(); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" {
			return ref, nil
		}
	}

	current, err := currentBranch(worktreePath)
	if err != nil {
		return "", err
	}
	for _, branch := range []string{"main", "master"} {
		if branch == current {
			continue
		}
		c := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		c.Dir = worktreePath
		if err := c.Run(); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("cannot resolve a local default branch ref")
}

// gitShowBlob reads path's content at ref. An empty ref reads the index's
// stage-0 blob, which is what `git show :path` means.
func gitShowBlob(worktreePath, ref, path string) ([]byte, error) {
	c := exec.Command("git", "show", ref+":"+path)
	c.Dir = worktreePath
	out, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s failed: %w", ref, path, err)
	}
	return out, nil
}

func branchIsMerged(clonePath, worktreePath string) (bool, error) {
	branch, err := currentBranch(worktreePath)
	if err != nil {
		return false, err
	}

	defaultBranch, err := defaultBranch(clonePath)
	if err != nil {
		return false, err
	}
	c := exec.Command("git", "branch", "--merged", defaultBranch)
	c.Dir = clonePath
	out, err := c.Output()
	if err != nil {
		return false, fmt.Errorf("git branch --merged %s failed: %w", defaultBranch, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "*+"))
		if line == branch {
			return true, nil
		}
	}
	return false, nil
}

func defaultBranch(clonePath string) (string, error) {
	c := exec.Command("git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	c.Dir = clonePath
	out, err := c.Output()
	if err == nil {
		branch := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		if branch != "" {
			return branch, nil
		}
	}

	c = exec.Command("git", "remote", "show", "origin")
	c.Dir = clonePath
	out, err = c.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "HEAD" && fields[1] == "branch:" && fields[2] != "" {
				return fields[2], nil
			}
		}
	}

	for _, branch := range []string{"main", "master"} {
		c = exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		c.Dir = clonePath
		if err := c.Run(); err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("resolve default branch failed")
}

func currentBranch(worktreePath string) (string, error) {
	c := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	c.Dir = worktreePath
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// closeTaskTab closes the task's tab, or the whole workspace if this was its last tab
// (herdr refuses to close a workspace's only tab directly).
//
// A tab that is no longer listed is already closed, which is this step's goal, so
// it is success and not an error: teardown removes several resources in sequence
// and any of the later steps can fail, so the whole command has to be runnable a
// second time without tripping over the work the first run already did.
func closeTaskTab(client *herdr.Client, workspaceID, tabID string) error {
	tabs, err := client.TabList(workspaceID)
	if err != nil {
		return err
	}
	found := false
	for _, tab := range tabs {
		if tab.TabID == tabID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	if len(tabs) == 1 {
		return client.WorkspaceClose(workspaceID)
	}
	return client.TabClose(tabID)
}
