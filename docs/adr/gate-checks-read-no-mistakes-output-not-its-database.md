# Gate checks read `no-mistakes`'s own output, never its database

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#92, atqamz/secondhand#97
- PRs: none single

## Context

A project registered with `--mode no-mistakes` expects its work to go through that gate.
`hand` never drives the gate: it does not call `axi run`, `axi respond` or `axi abort`.
The worker does.
So `hand`'s only stake is answering two questions before and after the fact.

Is the gate initialized for this repo?
`no-mistakes` keys its own state on the absolute `working_path` of the repo it was initialized against, and two ordinary histories orphan that row with nothing obliging anyone to notice: the fleet home gets renamed, moving every clone path at once, or a project is registered and `no-mistakes init` is never run.
Both leave a gated project silently ungated.

Did this shipped PR actually go through a run?
A project's gate can be ready and still never have run against the branch a PR came from, because the project was registered after the fact, the PR was opened by hand outside the `pr` step, or the check was bypassed.

Both answers are available two ways: parse what `no-mistakes` prints, or read `~/.no-mistakes/state.sqlite`.

## Decision

Both checks read `no-mistakes`'s own output text and never its database.
`GateStatus` runs `no-mistakes status` in the project's clone; `GateRunPRs` runs `no-mistakes runs --limit 10000` and collects the PR URL each `completed` row recorded for itself.

Every outcome is read from that text, including the ones that look like exit-code cases.
`no-mistakes status` always exits 0, initialized or not, and reports both orphaning histories with identical text, so the preflight does not try to tell them apart.

Failure outcomes are kept distinct rather than collapsed, because the remedies differ.
Not initialized is exit 3 naming `no-mistakes init` verbatim, since that command is idempotent and repairs a stale `working_path` in place.
A missing or unrunnable binary, a clone path that does not exist, and a clone path that is not a git repository are each exit 1 with their own message: the world is not in a state the operator fixes by initializing anything.

A question the check could not answer never renders as the stronger claim.
`unreachable` is its own bucket, distinct from `no run found`, and it covers a missing clone, an unrunnable binary, an uninitialized gate and a non-git path.

The gated marker says only that the `pr` step opened this exact PR from a run that reached `completed`, and the wording is deliberately no stronger.

## Rejected alternatives

**Read `~/.no-mistakes/state.sqlite` directly.**
It is another tool's private schema, with no compatibility promise and no version gate `hand` could check.
It is also more precise than the answer `hand` is entitled to give, which invites claims the data does not support.

**Trust `no-mistakes status`'s exit code.**
It always exits 0.
An uninitialized repo, a stale `working_path` and a non-git directory are all successes by that measure, and the last one used to read as a ready gate.

**Collapse every failure into "not initialized" and name one remedy.**
The remedy for a missing binary is not `no-mistakes init`, and telling an operator to run it sends them somewhere the problem is not.

**Let an uninitialized gate read as an empty run list.**
`no-mistakes` still holds that repo's completed runs, so an empty list would report a genuinely gated PR as never gated.
That is the single worst answer this check can give.

**Detect the missing clone path from the failed chdir.**
It surfaces as "binary not found or not runnable", which is misleading.
The path is stat-ed before the binary is run at all.

**Make the gated marker a per-commit or per-branch answer.**
`no-mistakes` keys on `working_path`, not per PR, and `hand` records no head commit to compare against.
A push after the matched run still reads as gated, and the wording admits that rather than implying otherwise.

## Consequences

Both checks are text scraping against another tool's output, so a wording change in `no-mistakes` breaks them.
That is the accepted cost, and it is why `internal/faketool` records the real output rather than a paraphrase.

`--skip-gate-check` bypasses the preflight and prints a warning to stderr naming the project, so a bypass is visible in the transcript rather than a silent env var.

`hand project list` runs the same check per project and carries the outcome in a `gate` column, so a stale gate is visible without waiting for a spawn to refuse.

`GateRunPRs` is answered per clone and cached for one render, so a fleet with several done ship tasks on one project pays one `no-mistakes` process rather than one per task.
