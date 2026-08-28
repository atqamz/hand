# Cross-repo PR delivery is an explicit opt-in

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#423
- PRs: none

## Context

`hand pr` refuses a URL whose repo is neither the project's own nor its declared upstream
(`internal/project/pr.go`, `ValidatePR`). The guard is right: a PR from an unrelated repo must
never reach task state by accident, since a recorded PR feeds `gh pr merge` directly. But the
guard has no escape for a delivery that really did land somewhere else.

`butler-broker-allowlist-rujak-release` was dispatched against project `yes2infra`. Between the
brief being written and the worker starting, `yes2infra#435` deleted the file the brief targeted
and moved the component to `yes2games/butler` - the same file, the same shape, the same gate,
verbatim, with an ADR update saying so. The worker followed the code and delivered
`yes2games/butler#92`. The outcome is correct. `hand pr` refused to record it, and the refusal's
own text names the only escape it offers: "no upstream is declared for it."

That escape is wrong for this case. `hand project upstream` declares the repo a *fork* opens its
PRs against - documented as exactly that, and load-bearing for every future PR on the project, not
just this one. `yes2infra` is not a fork of `butler`; they are sibling repositories in the same
organization. Declaring `butler` as `yes2infra`'s upstream would write a false topology into the
registry and change PR resolution for every future `yes2infra` task, to record one task's PR. The
refusal was steering an operator with a genuine one-off delivery toward a fix that is worse than
the missing record.

The safety property worth keeping is "not by accident," not "not at all."

## Decision

**`hand pr` grows an explicit `--cross-repo` flag, paired with a required `--reason`.** Passing
`--cross-repo` waives exactly the one check that compares the URL's repo against the project's own
repo and its declared upstream (`ValidatePR`'s new `crossRepo` parameter). Every other validation
runs unchanged: the URL still has to parse, the PR still has to exist and be observable on GitHub
(`ObserveMergeState`), and `hand pr` is still write-once - a second, different URL is refused
whether or not `--cross-repo` was used the first time. Nothing about the project registry changes;
`hand project upstream` still means exactly what it means, and fork PR resolution is untouched.

**The reason is required, not optional.** A cross-repo delivery is always a scope deviation worth
reading later, and an unrecorded reason is the one thing a later reader could never reconstruct -
the worker's free-text report is not durable state, and today it is the only place this survives at
all. `hand pr` refuses `--cross-repo` without `--reason`, and refuses `--reason` without
`--cross-repo`: the two only mean something together, so one arriving alone is refused rather than
silently accepted or silently ignored.

**The reason is a durable column, not folded into free text.** `task.pr_cross_repo_reason`
(`internal/store`, `crossRepoPRVersion`) is empty for every PR recorded against the project's own
repo or its declared upstream, and carries the operator's text only when `--cross-repo` wrote it.
Its mere non-emptiness already marks the record as a deliberate delivery elsewhere - no separate
boolean column, since one could read `true` with an empty reason after a bug elsewhere and no
column could ever drift out of sync with the other.

**The refusal itself no longer points at `hand project upstream`.** Before this change, a project
with no declared upstream had its refusal read "... and no upstream is declared for it" - true, but
read as an instruction, it sent an operator toward writing false topology. The refusal now names
`--cross-repo`/`--reason` as the escape, and still names the declared upstream when there is one,
since that remains the right answer for a real fork contribution.

**Every surface `hand status` renders a PR through marks a cross-repo record distinctly.** The
`pr` field carries the reason inline (`"<url> (cross-repo: <reason>)"`), `taskFlags` gains a
`cross-repo` token so the fleet view's default columns show it without opting into the `pr` field,
and `--json` carries `pr_cross_repo_reason` alongside `pr`. A record that looks identical to a
normal one would defeat the point of requiring the opt-in in the first place.

**The watcher's auto-recording path never sets `crossRepo`.** `autoRecordPR`
(`internal/watcher/watcher.go`) and `DetectPR`'s branch-based re-detection
(`internal/runtime/pr.go`) both call `RecordPR`/`ValidatePR` with `crossRepo` hardcoded to `false`.
A cross-repo delivery is asserted by an operator through the explicit flag, never inferred by hand
from a worker's report line or from re-detecting a branch it already pushed - the latter can only
ever find a PR on the project's own repo or its declared upstream, since that is where hand itself
pushed the branch.

## Rejected alternatives

- **Reusing or extending `hand project upstream`** - the thing this record explicitly rejects. It
  would conflate "this project is a fork" (a standing topological fact) with "this one task
  delivered somewhere else" (a one-off event), and the conflation is exactly what made the original
  escape wrong.
- **Making the reason optional** - considered, since every other free-text reason in `hand`
  (`hand deliver --reason`) is required for the same shape of argument, and the issue itself flags
  this as the reading expected to hold up. An unexplained cross-repo record is unrecoverable later;
  requiring it costs one flag at the moment the operator already knows the answer.
- **A boolean `pr_cross_repo` column alongside a separate reason column** - rejected for the
  possible-drift reason above: two columns can disagree, one column that is either empty or
  meaningful cannot.
- **Deriving "is this cross-repo" at render time by comparing the recorded URL's repo against the
  project's current repo and upstream** - rejected because it re-answers the question with
  *today's* topology rather than the one true at record time, and because it requires a live git
  call (`RepoSlug`) per row rendered rather than a stored fact. A project's upstream can change
  after the PR was recorded; the record must not flip retroactively.
- **Having `hand` detect a cross-repo delivery on its own** (e.g. from report-line PR mentions or
  from broader GitHub search) - out of scope by design. The supervisor asserts a cross-repo
  delivery; `hand` verifies it happened, exactly as it already verifies every other recorded PR.

## Consequences

The atqamz/hand#423 repro completes: `hand pr butler-broker-allowlist-rujak-release --cross-repo
--reason "..." https://github.com/yes2games/butler/pull/92` records the true outcome instead of
leaving `pr: unknown` forever, without writing any false fork topology into the registry.

`internal/project/pr.go`'s `ValidatePR` and `internal/runtime/pr.go`'s `RecordPR` both take an
explicit `crossRepo bool` now; every caller states its intent rather than inheriting a default.

atqamz/hand#422 remains unowned by this change - a different family member (a merge that already
landed) with no workaround at all, unlike this one's `hand deliver --reason` partial workaround.
