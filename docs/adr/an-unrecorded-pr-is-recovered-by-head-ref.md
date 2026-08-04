# An unrecorded PR is recovered by head ref alone, and ambiguity refuses

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#134, atqamz/secondhand#146
- PRs: none single

## Context

`hand pr` records a PR under a task.
A no-mistakes gate's own `pr` step opens a PR directly, so genuinely landed work routinely has no PR recorded against it, and a task whose PR is unrecorded reads as unshipped everywhere downstream: teardown refuses it, the merge poll has nothing to watch, and the gate check has no PR to ask about.

So `hand status` has to recover the PR from GitHub.
The recovery needs a key, and the only fact `hand` holds that GitHub also holds is the task's branch.

## Decision

The lookup matches on head ref alone, never on title, issue number or task id.
A title is prose a worker wrote and an issue number is a claim in a PR body: both are guesses about intent, and a wrong recovery records somebody else's PR as this task's work.
A head ref is what the branch is.

Matching on a branch name is not by itself unique, so three narrowings apply.

A declared `upstream` is searched as well as the project's repo, because a fork contribution's PR is opened on the upstream while the branch is pushed to the fork.
Only PRs whose head branch lives in the project's own repo count, because an upstream carries head refs from every contributor's fork and a stranger's same-named branch would otherwise be recorded here.
Every repo-slug comparison folds case, because a GitHub slug is unique only up to casing: `gh` reports GitHub's canonical casing while the clone's `origin` remote carries whatever the operator typed.
Compared exactly, the head-repo filter drops a landed PR, and an `upstream` naming the project's own repo in another casing is searched twice, which returns the one real PR as its own same-tier duplicate and makes it ambiguous (atqamz/secondhand#146).

Several PRs on one branch resolve by preference tier - merged, then open, then closed-unmerged - and only when the winning tier holds exactly one.
A merged PR coexisting with an open one on the same head ref refuses rather than resolving to the merged one, because an open PR is live evidence the branch may carry unlanded work.
Matches from both repos go through that one tier pass, so a fork whose upstream also carries a PR on that branch is ambiguous exactly like two PRs in one repo.

The whole lookup is best-effort and non-blocking, and it runs only in the single-task view.

## Rejected alternatives

**Match on the issue number in the PR body, or on the PR title.**
Both are authored text.
A worker who pasted the wrong number, or two PRs whose titles both name the same fix, produce a confident wrong answer, and the wrong answer is durable: it gets recorded.

**Resolve ambiguity by picking the newest PR, or the merged one.**
Picking the merged one over an open one is the case most likely to be wrong, because the open PR is the evidence that the branch is still moving.
Refusing costs one `hand pr` invocation; guessing wrong records a PR nobody will re-check.

**Normalize casing by rewriting the stored slug at registration time.**
It fixes projects registered afterwards and leaves every already-registered project broken, and the comparison still has to be correct for a slug that arrived from `gh`.
Fold at comparison, once, where both sides meet.

**Search the upstream without the head-repo filter.**
A popular upstream has head refs from every fork, so a common branch name recovers a stranger's PR.
That is worse than recovering nothing.

**Do the lookup in the fleet overview too.**
It is one `gh` call per unrecorded ship task, so a fleet-wide render pays for every task to answer a question about one.
The single-task view is where somebody is already asking about that task.

**Record the recovered PR only after asking the operator.**
The recovery exists because nobody noticed the gate had opened the PR.
A confirmation prompt puts the notice back in the path that already failed to happen.

## Consequences

A branch reused across tasks is unrecoverable by this route, and that is correct: the key genuinely does not identify one task.
Such a task needs `hand pr` run against it by hand.

The refusal cases are silent in the sense that nothing is recorded, and `hand status` reports what it read rather than what it declined to conclude.
An operator who expected a PR to appear and sees `none` has to run `hand pr`, and no output tells them ambiguity was the reason.
That is the accepted cost of not blocking a status render on a diagnosis.

Case-folding is the kind of bug that reappears wherever a slug is compared, so it is a property of every repo-slug comparison in this lookup rather than of the two sites where it was found broken.
