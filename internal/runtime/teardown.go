package runtime

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

	"github.com/atqamz/hand/internal/completion"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

func (r *Runtime) Teardown(ctx context.Context, req TeardownRequest) (Result, error) {
	if req.Context != nil {
		ctx = req.Context
	}
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := state.Lock(req.Home, "task:"+req.ID)
	if err != nil {
		return Result{}, fmt.Errorf("lock task %q: %w", req.ID, err)
	}
	defer release()

	history, err := state.ReadHistory(req.Home, req.ID)
	if err != nil {
		if errors.Is(err, state.ErrTaskNotFound) {
			return Result{}, Precondition(err)
		}
		return Result{}, err
	}
	if history.ActiveAttempt == nil {
		return Result{}, Precondition(fmt.Errorf("task %q has no active attempt", req.ID))
	}
	task := history.Task
	originalTask := task
	active := *history.ActiveAttempt
	warnings := []string{}
	fail := func(err error) (Result, error) { return Result{}, WithWarnings(err, warnings) }
	launched := active.Lifecycle != state.AttemptProvisioning || active.LaunchSubmittedAt != "" || active.LaunchConfirmedAt != ""
	if active.TeardownCompletionState != "" {
		record, found, err := completion.FindAttempt(req.Home, active.ID)
		if err != nil {
			return fail(fmt.Errorf("recover completion record: %w", err))
		}
		if found {
			if active.TeardownCompletionState != state.TeardownCompletionAppended {
				if err := state.SetAttemptTeardownCompletionState(req.Home, req.ID, active.ID, active.Lifecycle, state.TeardownCompletionAppended); err != nil {
					return fail(fmt.Errorf("record recovered completion state: %w", err))
				}
			}
			return r.finishTeardown(req, record, active, warnings)
		}
		if active.TeardownCompletionState == state.TeardownCompletionAppended {
			return fail(fmt.Errorf("completion state for attempt %d has no exact completion record", active.ID))
		}
	}
	dirtWasSafe := false
	terminalAttempt := active.TeardownTerminalAttempt
	disposition := active.TeardownDisposition
	if disposition == state.TeardownDispositionCompletedSafeDirt {
		dirtWasSafe = true
	}
	if terminalAttempt == "" && !req.Force && launched {
		if active.Lifecycle == state.AttemptProvisioning && active.Worktree == "" {
			return fail(Precondition(fmt.Errorf("task %q has launch evidence but no worktree to inspect; rerun with --force to interrupt it", req.ID)))
		}
		updated, safeDirt, err := checkLandedWork(ctx, req.Home, task, active)
		if err != nil {
			return fail(err)
		}
		task = updated
		dirtWasSafe = safeDirt
	}
	if terminalAttempt == "" {
		terminalAttempt, disposition = teardownDecision(req.Force, launched, active.Lifecycle, dirtWasSafe)
		if err := state.SetAttemptTeardownDecision(req.Home, req.ID, active.ID, terminalAttempt, disposition); err != nil {
			return fail(fmt.Errorf("record teardown decision: %w", err))
		}
	} else if disposition == "" {
		return fail(fmt.Errorf("attempt %d has teardown lifecycle without disposition", active.ID))
	}
	releaseProject, err := state.Lock(req.Home, "project:"+task.Project)
	if err != nil {
		return fail(fmt.Errorf("lock project %q: %w", task.Project, err))
	}
	defer releaseProject()

	if err := r.releaseHerdr(req.Home, req.ID, active, &warnings); err != nil {
		return fail(err)
	}
	if err := r.afterPhase(phaseHerdrReleased); err != nil {
		return fail(err)
	}
	returnForce := teardownReturnForce(req.Force, dirtWasSafe, disposition)
	if err := r.releaseWorktree(req.Home, req.ID, active, returnForce); err != nil {
		return fail(err)
	}

	record := completionFor(task, disposition, launched)
	record.AttemptID = active.ID
	record.AttemptLifecycle = string(terminalAttempt)
	record.TornDownAt = r.deps.now().Format(time.RFC3339)
	if task.PR != originalTask.PR {
		if err := state.SetTaskPR(req.Home, req.ID, task.PR); err != nil {
			return fail(fmt.Errorf("record task facts: %w", err))
		}
	}
	if task.Kind != originalTask.Kind {
		if err := state.SetTaskKind(req.Home, req.ID, string(task.Kind)); err != nil {
			return fail(fmt.Errorf("record task kind: %w", err))
		}
	}
	if task.MergeAnnounced && !originalTask.MergeAnnounced {
		if err := state.SetTaskMergeAnnounced(req.Home, req.ID); err != nil {
			return fail(fmt.Errorf("record merge announcement: %w", err))
		}
	}
	if err := state.SetAttemptTeardownCompletionState(req.Home, req.ID, active.ID, active.Lifecycle, state.TeardownCompletionPending); err != nil {
		return fail(fmt.Errorf("record completion phase: %w", err))
	}
	if err := r.deps.appendCompletion(req.Home, record); err != nil {
		return fail(fmt.Errorf("record completion: %w", err))
	}
	if err := state.SetAttemptTeardownCompletionState(req.Home, req.ID, active.ID, active.Lifecycle, state.TeardownCompletionAppended); err != nil {
		return fail(fmt.Errorf("record completion evidence: %w", err))
	}
	if err := r.afterPhase(phaseCompletionAppended); err != nil {
		return fail(err)
	}
	return r.finishTeardown(req, record, active, warnings)
}

func teardownReturnForce(requestForce, dirtWasSafe bool, disposition string) bool {
	return requestForce || dirtWasSafe || disposition == state.TeardownDispositionForced || disposition == state.TeardownDispositionCompletedSafeDirt
}

func (r *Runtime) finishTeardown(req TeardownRequest, record completion.Record, active state.Attempt, warnings []string) (Result, error) {
	terminalAttempt := active.TeardownTerminalAttempt
	if terminalAttempt == "" && record.AttemptLifecycle != "" {
		terminalAttempt = state.AttemptLifecycle(record.AttemptLifecycle)
	}
	if terminalAttempt == "" {
		return Result{}, WithWarnings(fmt.Errorf("attempt %d has no durable teardown terminal lifecycle", active.ID), warnings)
	}
	if err := state.TerminalizeTaskAndAttempt(req.Home, req.ID, active.ID, active.Lifecycle, terminalAttempt); err != nil {
		return Result{}, WithWarnings(fmt.Errorf("record task completion: %w", err), warnings)
	}
	if err := state.ClearHoldIfKind(req.Home, req.ID, state.HoldKindLimit); err != nil {
		warnings = append(warnings, fmt.Sprintf("warning: clear usage-limit hold failed: %v", err))
	}
	worktreeResult := "returned"
	if active.Worktree == "" {
		worktreeResult = "none"
	}
	return Result{
		ID: req.ID, Project: record.Project, Kind: record.Kind, Outcome: record.Outcome, Detail: record.Detail, Worktree: worktreeResult,
		Warnings: warnings, Help: []string{"This task remains inspectable with `hand status " + req.ID + "`; use `hand reopen " + req.ID + "` for another attempt"},
	}, nil
}

func (r *Runtime) releaseHerdr(home, taskID string, attempt state.Attempt, warnings *[]string) error {
	if attempt.Herdr.WorkspaceID == "" && attempt.Herdr.TabID == "" && attempt.Herdr.PaneID == "" {
		return nil
	}
	if attempt.TeardownHerdrState == state.TeardownResourceReleased {
		return nil
	}
	if attempt.TeardownHerdrState == state.TeardownResourceReleasing || attempt.TeardownHerdrState == state.TeardownResourceAmbiguous {
		*warnings = append(*warnings, fmt.Sprintf("warning: Herdr ownership for attempt %d is ambiguous; refusing destructive retry", attempt.ID))
		return nil
	}
	if err := incompleteHerdrOwnership(attempt.Herdr); err != nil {
		if stateErr := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceAmbiguous); stateErr != nil {
			return fmt.Errorf("record incomplete Herdr ownership: %w", stateErr)
		}
		*warnings = append(*warnings, fmt.Sprintf("warning: Herdr ownership incomplete: %v", err))
		return nil
	}
	if err := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleasing); err != nil {
		return fmt.Errorf("record Herdr release phase: %w", err)
	}
	if err := closeTaskTab(r.deps.herdr(), attempt.Herdr.WorkspaceID, attempt.Herdr.TabID); err != nil {
		if stateErr := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceAmbiguous); stateErr != nil {
			return fmt.Errorf("record failed Herdr release: %w", stateErr)
		}
		*warnings = append(*warnings, fmt.Sprintf("warning: herdr tab close failed: %v", err))
		return nil
	}
	if err := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "herdr", state.TeardownResourceReleased); err != nil {
		return fmt.Errorf("record Herdr release evidence: %w", err)
	}
	return nil
}

func (r *Runtime) releaseWorktree(home, taskID string, attempt state.Attempt, force bool) error {
	if attempt.Worktree == "" {
		return nil
	}
	verifyLease := r.deps.worktree.verifyLease
	if verifyLease == nil {
		verifyLease = worktree.VerifyLease
	}
	verifyOwnership := func(action string) error {
		if err := verifyLease(attempt.Worktree, attempt.LeaseID); err != nil {
			if stateErr := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceAmbiguous); stateErr != nil {
				return fmt.Errorf("record unverified worktree ownership: %w", stateErr)
			}
			return fmt.Errorf("verify worktree ownership before %s: %w", action, err)
		}
		return nil
	}
	switch attempt.TeardownWorktreeState {
	case state.TeardownResourceReleased:
		return nil
	case state.TeardownResourceReleasing, state.TeardownResourceAmbiguous:
		return fmt.Errorf("worktree ownership for attempt %d is ambiguous; refusing destructive retry", attempt.ID)
	case state.TeardownResourceRetryable:
		if !force {
			return fmt.Errorf("worktree for attempt %d remains leased; retry with --force", attempt.ID)
		}
		if err := verifyOwnership("forced retry"); err != nil {
			return err
		}
	}
	// Legacy attempts without a persisted lease ID retain pre-migration path-only behavior.
	if attempt.TeardownWorktreeState == "" && attempt.LeaseID != "" {
		if err := verifyOwnership("return"); err != nil {
			return err
		}
	}
	if err := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleasing); err != nil {
		return fmt.Errorf("record worktree release phase: %w", err)
	}
	if err := r.deps.worktree.returnWorktree(attempt.Worktree, force); err != nil {
		if errors.Is(err, worktree.ErrReturnAborted) {
			if stateErr := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceRetryable); stateErr != nil {
				return fmt.Errorf("record retryable worktree return: %w", stateErr)
			}
			return err
		}
		if stateErr := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceAmbiguous); stateErr != nil {
			return fmt.Errorf("record failed worktree release: %w", stateErr)
		}
		return err
	}
	if err := state.SetAttemptTeardownResourceState(home, taskID, attempt.ID, attempt.Lifecycle, "worktree", state.TeardownResourceReleased); err != nil {
		return fmt.Errorf("record worktree release evidence: %w", err)
	}
	if err := r.afterPhase(phaseWorktreeReturned); err != nil {
		return err
	}
	return nil
}
func teardownDecision(forced, launched bool, lifecycle state.AttemptLifecycle, dirtWasSafe bool) (state.AttemptLifecycle, string) {
	if forced {
		return state.AttemptInterrupted, state.TeardownDispositionForced
	}
	if !launched {
		return state.AttemptInterrupted, state.TeardownDispositionNeverLaunched
	}
	if lifecycle == state.AttemptProvisioning {
		return state.AttemptInterrupted, state.TeardownDispositionLaunchedProvisioning
	}
	if dirtWasSafe {
		return state.AttemptCompleted, state.TeardownDispositionCompletedSafeDirt
	}
	return state.AttemptCompleted, state.TeardownDispositionCompleted
}

func completionFor(t state.Task, disposition string, launched bool) completion.Record {
	c := completion.Record{ID: t.ID, Project: t.Project, Kind: t.Kind}
	// The delivered case sits ahead of the merge cases below only while no merge is on the row: a
	// delivery that then genuinely landed has the stronger fact to record, so an observed or executed
	// merge outranks the mark.
	switch {
	// Ahead of every case below, including the forced one: none of them can be true of an attempt
	// whose agent never started, and recording a merge or a delivery for it would be a false fact.
	case disposition == state.TeardownDispositionNeverLaunched || !launched:
		c.Outcome = "torn-down"
		c.Detail = "attempt never launched"
	case disposition == state.TeardownDispositionForced:
		c.Outcome = "torn-down"
		c.Detail = "forced (landed-work checks skipped)"
	// A task whose landing was never ours to decide has to stay distinguishable from a merged one in
	// the permanent record, or the fleet's history claims upstream merges that never happened
	// (atqamz/hand#78).
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
func checkLandedWork(ctx context.Context, home string, t state.Task, active state.Attempt) (state.Task, bool, error) {
	if t.Kind == state.KindScout {
		reportPath := filepath.Join("data", t.ID, "report.md")
		if _, err := os.Stat(filepath.Join(home, reportPath)); err != nil {
			return t, false, Precondition(fmt.Errorf("report not found at %s", reportPath))
		}
		return t, false, nil
	}

	status, err := gitStatusPorcelain(active.Worktree)
	if err != nil {
		return t, false, err
	}
	dirtWasSafe := false
	if status != "" {
		if !dirtIsSafeToDiscard(active.Worktree, status) {
			return t, false, Precondition(fmt.Errorf("uncommitted changes in worktree %s:\n%s", active.Worktree, capStatusLines(status)))
		}
		dirtWasSafe = true
	}

	// Every check below asks "did this land", which for a contribution offered to someone else's repo
	// hand cannot answer and the fleet does not decide. A recorded delivery answers what teardown needs
	// instead - the work is out of this worktree and accounted for - so it is terminal here.

	// Terminal without --force too, and recorded as delivered rather than merged
	// (atqamz/hand#78). Placed after the dirt check and the scout report check deliberately:
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
			merged, err := branchIsMerged(filepath.Join(home, "projects", t.Project), active.Worktree)
			if err != nil {
				return t, false, err
			}
			if !merged {
				return t, false, Precondition(fmt.Errorf("branch for %s is not merged into the default branch", t.ID))
			}
			return t, dirtWasSafe, nil
		}

		// A gate-opened PR bypasses hand pr entirely (atqamz/hand#69), so t.PR can still be empty
		// for landed work; detect it here rather than only refusing on it, so the merged check below
		// reads the same PR state hand pr would have recorded.
		if exists {
			detected, err := DetectPR(ctx, home, t, active, proj)
			var ambiguous *ghutil.AmbiguousPRError
			// An ambiguous branch is a different failure and must not fall through the same way: "no PR
			// recorded" reads as unlanded, but ambiguous means unknown, and picking either meaning here
			// for the operator is the guess atqamz/hand#77 exists to remove.
			if errors.As(err, &ambiguous) {
				return t, false, Precondition(fmt.Errorf("PR for %s is ambiguous, refusing to guess: %w", t.ID, ambiguous))
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
			// (atqamz/hand#129), so a scout spawned without --scout arrives here as a ship row and
			// refuses on a PR it was never going to open. The guard reads the work rather than the record.

			// Here rather than earlier so it can only decide the case nothing else claims: a delivery, a
			// local-only merge, and a gate-opened PR all return above it, so reaching this line means no PR
			// exists to shadow.

			// A report deliverable on disk and a branch carrying no commits of its own is a completed scout
			// whatever the row says, and is recorded as one. Merge evidence excludes the path outright - work
			// hand merged or watched merge landed as a merge, and the record has to say so (atqamz/hand#78).
			if !t.MergeExecuted && !t.MergeAnnounced && isCompletedScout(home, t, active.Worktree) {
				t.Kind = state.KindScout
				return t, dirtWasSafe, nil
			}
			// Narrow on purpose: a ship task whose PR was never opened still has its commits, so it still
			// refuses - the half of this guard that is load-bearing.
			return t, false, Precondition(fmt.Errorf("no PR recorded for %s and project is not local-only: work may not be landed", t.ID))
		}
	}

	merged, err := ghutil.PRIsMerged(ctx, t.PR)
	if err != nil {
		return t, false, err
	}
	if !merged {
		return t, false, Precondition(fmt.Errorf("PR %s is not merged", t.PR))
	}
	return t, dirtWasSafe, nil
}

// Reports whether a task's work is a delivered scout report and nothing else: the report exists under
// data/<id>/ and the worktree's branch adds no commit to the local default branch. Both halves are
// required - a report alone says nothing about code sitting unlanded on the branch beside it.
func isCompletedScout(home string, t state.Task, worktree string) bool {
	if _, err := os.Stat(filepath.Join(home, "data", t.ID, "report.md")); err != nil {
		return false
	}
	// Resolution failures fail closed, and the branch comparison is local-only like dirtIsSafeToDiscard's:
	// a stale local ref only misses a real case, it never accepts an unlanded one.
	baseRef, err := localDefaultBranchRef(worktree)
	if err != nil {
		return false
	}
	c := exec.Command("git", "rev-list", "--count", baseRef+"..HEAD")
	c.Dir = worktree
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

// Bounds the refusal's git status dump (atqamz/hand#65 is the same lesson for report rendering):
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
// byte: the gate re-editing a file to what its own merged fix carries (atqamz/hand#79).
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
