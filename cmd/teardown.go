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
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
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
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
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

			// treehouse refuses to clean a dirty worktree without --force, and nothing is left to answer
			// its prompt here, so dirt this command already judged discardable has to be returned
			// forcibly or the slot goes back to the pool still dirty.
			if err := worktree.Return(t.Worktree, force || dirtWasSafe); err != nil {
				return err
			}

			// Everything the record claims (landed work, a returned worktree) is already true by this
			// line, --force or not, so a fault after it lands cannot make the record inaccurate, only
			// late to remove its source.
			record := completionFor(t, force)
			record.TornDownAt = time.Now().UTC().Format(time.RFC3339)

			// Recorded before state.Delete, not after: the record is derived from t, which state.Delete
			// would remove out from under us. Failing here leaves the task row untouched, so the whole
			// command is simply retryable and no completion is lost.
			if err := completion.Append(home, record); err != nil {
				return fmt.Errorf("record completion: %w", err)
			}

			// Failing here leaves the task row in place, so a retry replays the whole command and
			// appends a second, identical record - a harmless duplicate this trades for never losing a
			// completion.
			if err := state.Delete(home, id); err != nil {
				return asPrecondition(err)
			}

			// A hold outlives the task row it was set on, which is what an operator hold is for. A limit hold
			// is the opposite: nothing is left to resume and no watcher will ever clear it, so left behind it
			// would refuse `hand spawn` on this id forever.
			if err := state.ClearHoldIfKind(home, id, state.HoldKindLimit); err != nil {
				// A warning rather than a failure: the teardown itself is done, and re-running it cannot undo
				// the delete.
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: clear usage-limit hold failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "torn-down")
			doc.Field("project", record.Project)
			doc.Field("kind", record.Kind)
			doc.Field("outcome", record.Outcome)
			doc.Field("detail", orNone(record.Detail))
			doc.Field("worktree", "returned")
			doc.Help("This id is gone from `hand status`; its completion is the last word on it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip landed-work checks")
	return cmd
}

func completionFor(t state.Task, forced bool) completion.Record {
	c := completion.Record{ID: t.ID, Project: t.Project, Kind: t.Kind}
	// The delivered case sits ahead of the merge cases below only while no merge is on the row: a
	// delivery that then genuinely landed has the stronger fact to record, so an observed or executed
	// merge outranks the mark.
	switch {
	case forced:
		c.Outcome = "torn-down"
		c.Detail = "forced (landed-work checks skipped)"
	// A task whose landing was never ours to decide has to stay distinguishable from a merged one in
	// the permanent record, or the fleet's history claims upstream merges that never happened
	// (atqamz/secondhand#78).
	case t.DeliveredAt != "" && !t.MergeExecuted && !t.MergeAnnounced:
		c.Outcome = "delivered"
		c.Detail = t.DeliveredReason
		if t.PR != "" {
			c.Detail = "PR " + t.PR + ": " + t.DeliveredReason
		}
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

// Reports whether the task's work is landed, and whether it got there past dirt it judged safe to
// discard - the caller has to force the worktree return in that case, since treehouse will not clean a
// dirty worktree on its own.
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

	// Every check below asks "did this land", which for a contribution offered to someone else's repo
	// hand cannot answer and the fleet does not decide. A recorded delivery answers what teardown needs
	// instead - the work is out of this worktree and accounted for - so it is terminal here.

	// Terminal without --force too, and recorded as delivered rather than merged
	// (atqamz/secondhand#78). Placed after the dirt check and the scout report check deliberately:
	// --force keeps its one meaning of discarding work nobody delivered, so both of those still refuse.
	if t.DeliveredAt != "" {
		return t, dirtWasSafe, nil
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

		// A gate-opened PR bypasses hand pr entirely (atqamz/secondhand#69), so t.PR can still be empty
		// for landed work; detect it here rather than only refusing on it, so the merged check below
		// reads the same PR state hand pr would have recorded.
		if exists {
			detected, err := detectPR(ctx, home, t, proj)
			var ambiguous *ghutil.AmbiguousPRError
			// An ambiguous branch is a different failure and must not fall through the same way: "no PR
			// recorded" reads as unlanded, but ambiguous means unknown, and picking either meaning here
			// for the operator is the guess atqamz/secondhand#77 exists to remove.
			if errors.As(err, &ambiguous) {
				return t, false, &ExitError{Err: fmt.Errorf("PR for %s is ambiguous, refusing to guess: %w", t.ID, ambiguous), Code: 3}
			}
			// Detection failing (no clone on disk yet, gh unreachable, ...) is not this command's failure
			// to report: it falls through to the same "no PR recorded" refusal below that a project with
			// no detection at all would have gotten.
			if err == nil {
				t = detected
			}
		}

		if t.PR == "" {
			// kind is the one field spawn records that nothing can correct afterwards
			// (atqamz/secondhand#129), so a scout spawned without --scout arrives here as a ship row and
			// refuses on a PR it was never going to open. The guard reads the work rather than the record.

			// Here rather than earlier so it can only decide the case nothing else claims: a delivery, a
			// local-only merge, and a gate-opened PR all return above it, so reaching this line means no PR
			// exists to shadow.

			// A report deliverable on disk and a branch carrying no commits of its own is a completed scout
			// whatever the row says, and is recorded as one. Merge evidence excludes the path outright - work
			// hand merged or watched merge landed as a merge, and the record has to say so (atqamz/secondhand#78).
			if !t.MergeExecuted && !t.MergeAnnounced && isCompletedScout(home, t) {
				t.Kind = state.KindScout
				return t, dirtWasSafe, nil
			}
			// Narrow on purpose: a ship task whose PR was never opened still has its commits, so it still
			// refuses - the half of this guard that is load-bearing.
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

// Reports whether a task's work is a delivered scout report and nothing else: the report exists under
// data/<id>/ and the worktree's branch adds no commit to the local default branch. Both halves are
// required - a report alone says nothing about code sitting unlanded on the branch beside it.
func isCompletedScout(home string, t state.Task) bool {
	if _, err := os.Stat(filepath.Join(home, "data", t.ID, "report.md")); err != nil {
		return false
	}
	// Resolution failures fail closed, and the branch comparison is local-only like dirtIsSafeToDiscard's:
	// a stale local ref only misses a real case, it never accepts an unlanded one.
	baseRef, err := localDefaultBranchRef(t.Worktree)
	if err != nil {
		return false
	}
	c := exec.Command("git", "rev-list", "--count", baseRef+"..HEAD")
	c.Dir = t.Worktree
	out, err := c.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "0"
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

// Bounds the refusal's git status dump (atqamz/secondhand#65 is the same lesson for report rendering):
// an unbounded dump into a session is its own problem, so this prints the first N entries and a count
// of the rest.
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

// Reports whether every uncommitted change in status - the worktree's `git status --porcelain` output -
// is a tracked modification whose content already matches the local default branch's tip, byte for
// byte: the gate re-editing a file to what its own merged fix carries (atqamz/secondhand#79).
func dirtIsSafeToDiscard(worktreePath, status string) bool {
	if status == "" {
		return true
	}
	// Every failure to resolve, read, or parse below fails closed - the caller gets the ordinary
	// refusal, never a discard on unverified dirt. Resolution is local-only, no fetch: a stale local ref
	// just means a real safe case is missed.
	baseRef, err := localDefaultBranchRef(worktreePath)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			return false
		}
		indexState, workingState, path := line[0], line[1], line[3:]
		// Untracked files are never safe: there is nothing in the base to compare them against, so their
		// mere presence fails this check.
		if indexState == '?' || workingState == '?' {
			return false
		}
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+len(" -> "):]
		}

		// Actual bytes, not the path: checking only that the path exists in the base, or comparing paths
		// alone, both pass cases that lose data - a same-named file with different content, or a path
		// that only coincidentally matches.
		base, err := gitShowBlob(worktreePath, baseRef, path)
		if err != nil {
			return false
		}
		// Each porcelain line reports two layers, and a change at either is content teardown would throw
		// away, so each is compared against the base on its own: an "MM" path whose working copy matches
		// the base still hides a third, differing version staged in the index.
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
	// Discarding dirt of this shape on teardown loses nothing.
	return true
}

// Resolves the worktree's local knowledge of the default branch without touching the network: a real
// treehouse worktree shares its refs with the project clone it was leased from, so a prior fetch there
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

// Reads path's content at ref. An empty ref reads the index's stage-0 blob, which is what
// `git show :path` means.
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

// Closes the task's tab, or the whole workspace when this was its last tab. herdr closes the
// workspace either way, so this says so rather than leaving it to a side effect
// (internal/faketool/FIDELITY.md).
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
	// A tab that is no longer listed is already closed, which is this step's goal, so it is success and
	// not an error: teardown removes several resources in sequence and any later step can fail, so the
	// whole command has to be runnable again without tripping over the work the first run already did.
	if !found {
		return nil
	}
	// Reached only while the tab is still listed, so a rerun after the first run closed it never reads
	// the one tab left as this workspace's last and closes another task's workspace.
	if len(tabs) == 1 {
		return client.WorkspaceClose(workspaceID)
	}
	return client.TabClose(tabID)
}
