# Terminal release closes on evidence, not a new escape hatch

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#424, atqamz/hand#428
- PRs: none

## Context

`hand teardown` withholds a worktree until commit safety is proven durable. atqamz/hand#424 found
the refusal text a model of that discipline: it names the observation it ran (`git rev-list --count
HEAD --not --remotes`), distinguishes "unanswered" from "at risk", and names the missing evidence -
a recorded pull request. But the remedy it names was unreachable. `hand pr` refused a torn-down task
("run hand reopen first"), `hand reconcile --abandon-worktree` correctly no-op'd on a lease it could
still observe, and `hand reopen` was the only door left - which invents a new attempt to record a
fact about an old one, exactly what the first refusal exists to avoid. Each precondition was
individually defensible; together they were unsatisfiable.

atqamz/hand#428 found a narrower version of the same shape one step earlier. A pool slot starts on a
detached HEAD; a branch exists only once a worker creates one. A worker that paused, or was stopped
before committing, leaves `active.Branch` empty and the worktree detached. `internal/runtime/pr.go`
answered `unknown` for a pull request there - correctly, since a detached worktree cannot in general
answer at all. But hand already holds two observations that together disprove a pull request rather
than leaving it unknown: no branch was ever recorded to have opened one from, and
`worktree.ObserveCommitSafety`'s own primitive, `git rev-list --count HEAD --not --remotes`, already
proves no commit here is missing from a remote-tracking ref. `unknown` was a false negative, and it
was the only thing standing between a provably empty worktree and its release.

Both gates are the same worktree-release gate approached from different callers:
`resolveWorktreeCommitSafety` (used once a task is already terminal) and `checkLandedWork` (used
while it is still open). Fixing one without the other leaves the same shape of dead end on the
caller not fixed.

## Decision

Three changes, each supplying evidence a gate already asks for rather than weakening what counts as
proof.

**`internal/runtime/pr.go` proves absence from observations hand already holds.**
`observeDetachedHeadAbsence` composes two primitives - `git.IsDetachedHead` (HEAD names a commit
directly, not a branch) and `worktree.LocalOnlyCommitCount` (zero commits missing from a
remote-tracking ref) - and only when durable `active.Branch` is also empty. All three hold or the
observation stays `ghutil.ObservationUnknown`, unchanged from before. `git.IsDetachedHead` is its own
primitive rather than a read of `CurrentBranch`'s error, because `CurrentBranch` collapses "detached"
and "unreadable for any other reason" into one message; the absence proof needs to know which.

**`hand pr` (`RecordPR`) no longer refuses a terminal task.** Recording where work landed is not
resuming it: the command is already write-once (a second, different URL is still refused) and
already validates the URL against the project's repo and against GitHub before writing anything.
`state.SetTaskPR` touches no lifecycle column, so nothing about this write can leave the terminal
state - there is no code path from "task.PR changed" to "task reopened."

**`checkLandedWork` (teardown.go) treats the same three-condition proof as "nothing to land."** An
attempt that never created a branch and holds no commit missing from a remote-tracking ref produced
no committable work, so neither the local-only-merge check nor a PR search has anything to answer.
This is recorded under the existing `worker-exited-unlanded` disposition - the same one reconcile's
own convergence already uses for a worker that produced no landed work - rather than a bare boolean,
because disposition is what survives a retry that skips `checkLandedWork` entirely
(`active.TeardownDisposition`, read back before any lifecycle decision is made). Without that, a
retried teardown reaching `completionFor` without ever calling `checkLandedWork` again would fall
through to its `default` case and record "branch merged" for an attempt that never made one.

## Rejected alternatives

- **A teardown attestation for the commit-safety question** (atqamz/hand#424's second ask): an
  explicit flag with `--abandon-worktree`'s own discipline - refused wherever the question can be
  observed, usable only where hand is genuinely blind. Rejected because it adds a second way to
  satisfy a safety gate, and an attestation is only safe while hand is genuinely blind to the
  question it answers. `internal/runtime/pr.go`'s fix and `hand pr`'s new reach together shrink that
  blind spot; adding an attestation anyway would make it the path of least resistance for a question
  hand can now often answer itself, which is the shape
  [Every diagnosis names a reachable treatment](every-diagnosis-names-a-reachable-treatment.md)
  already rejected for other gates.
- **Weakening `resolveWorktreeCommitSafety` or the commit-safety gate itself** - never on the table.
  The defect in both issues is that a reachable remedy was missing, not that the gate asked for too
  much; nothing here changes what counts as proof of durability.
- **Inferring "no work" in `checkLandedWork` from `task.PR == ""` alone** - already the pre-existing,
  narrower refusal ("a ship task whose PR was never opened still has its commits") and it must stay
  narrow: only the full three-condition proof, not a bare empty PR field, licenses skipping the
  landed-work question, or a branch carrying real unlanded commits would release unrefused.
  `TestObservePRStaysUnknownWhenABranchIsRecordedDespiteDetachedHeadWithNoLocalOnlyCommits` pins the
  boundary: a recorded branch still goes to GitHub, never to the local shortcut.
  `TestTeardownStillRefusesAShipTaskWhosePRWasNeverOpened` (`cmd/teardown_test.go`) is unchanged.
- **A new `TeardownDisposition` value for "no committable work"** - rejected in favor of reusing
  `worker-exited-unlanded`, whose outcome text ("unlanded", "no landed work was observed") already
  describes this case honestly without growing the persisted vocabulary.
- **Recomputing "no committable work" from the worktree after release** - unsafe: `runTeardown`
  releases the worktree back to its pool before building the completion record, and a released slot
  may already be leased to a different attempt by the time a later step would re-inspect it. The
  proof is read once, inside `checkLandedWork`, before release, and threaded through disposition.

## Consequences

Proven absence of a pull request and an unobservable one stay distinguishable everywhere `observePR`
answers, exactly as atqamz/hand#407/#415 established for the callers that already existed.

A torn-down task can record where its work landed and gains no liveness by doing so: lifecycle stays
`terminal`, and `taskFlags`/`needsAttention` (`cmd/statusview.go`, the fleet's one attention
definition) never key off `task.PR`, so no surface - `hand status`, `hand orient`, next-action
ranking - starts treating it as actionable.

The atqamz/hand#424 repro completes without `hand reopen`
(`TestTeardownCircleClosesOnceHandPRRecordsTheEvidenceItAsksFor`), and the atqamz/hand#428 repro
releases with no flags at all (`TestTeardownReleasesADetachedWorktreeWithNoBranchAndNoLocalOnlyCommits`).

atqamz/hand#422 and atqamz/hand#423 remain unowned by this change: both touch `hand pr`'s validation
for a different reason (a merge already landed, a cross-repo PR), and neither needed the lifecycle
gate this record removes.
