# Liveness is observed, not assumed from launch

- Date: 2026-08-20
- Status: accepted
- Issues: atqamz/hand#259 (supersedes atqamz/hand#255), atqamz/hand#136, atqamz/hand#252, atqamz/hand#239, atqamz/hand#258
- PRs: none

## Context

`hand spawn` recorded `attempt_lifecycle: running` once a pane existed and a launch was issued, and nothing after that re-observed the attempt. Two real launches on 2026-08-18 died on their first turn - one on a transient upstream 529, one on an exhausted harness quota - and both stayed idle at a live pane for seven hours while Hand kept reporting the durable truth as running and healthy. `hand reconcile`'s observation of a running Attempt read only Herdr ownership (does this pane still belong to the persisted identity, does its agent name match), never Herdr's own `agent_status`, so a pane proven to belong to the right harness was `Keep`, whatever it was doing.

Two mechanisms already existed nearby and both assumed a live observer. `internal/watcher/events.go`'s `ClassifyStatus` and `ClassifyCatchUp` already compute the identical fact - idle with nothing on the report channel explaining it - but only from a poll tick inside a running `hand watch`, or from its arm-time catch-up. `internal/watcher/usagelimit.go` already detects and schedules a claude quota stop, but only from the same live poll loop. Neither fires when no watcher process exists, which is exactly what happened here.

atqamz/hand#239 already converges a running Attempt to a terminal lifecycle when its Herdr pane is observed absent, without an operator running `hand teardown`. atqamz/hand#258 already records "this attempt's worker took no turn" as a distinct, non-destructive teardown disposition, `worker-never-started`, reachable through an explicit attestation because whether an idle, still-present worker will resume was not something reconcile could observe. atqamz/hand#252 landed a level-triggered wake for a supervisor, but it reads durable fleet truth as its contract requires - it cannot wake on a fact nothing ever made durable.

## Decision

`hand reconcile` reads Herdr's `agent_status` for a running Attempt whose pane it already proves belongs to the persisted harness, and classifies it against the same rule `ClassifyStatus`/`ClassifyCatchUp` use: idle or done, with the last report state empty or still `working`, is idle-unreported; anything a report already explains, or a working/blocked pane, is not. A grace window past `launch_confirmed_at` withholds the classification for a harness that has not yet had time to leave its launch-confirmation quiet window, so an ordinary launch is never misread as a stall.

The classification is recorded durably in `status_changed_at`/`status_changed_for` - the same columns [arming a watch observes before it waits](arming-a-watch-observes-before-it-waits.md) already reads for its catch-up - so the fact survives whether reconcile or a live watcher observed it first, and a supervisor relying on either sees the same truth. `hand reconcile`'s own report carries the classification as `liveness`, so an operator or supervisor invoking it directly sees the fact without needing `hand status`'s separate live Herdr read.

An idle-unreported Attempt whose harness carries a catalogued usage-limit signature ([usage-limit detection is a harness capability](usage-limit-detection-is-a-harness-capability.md)) is additionally probed once for that signature, and only once per stop: reconcile skips the probe once either `usage_limit_retry_at` already holds a durable schedule, or `status_changed_for` shows this same idle pane was already probed and found stuck with no reset time stated. A detected limit is recorded the same way the watcher's own detection would: `usage_limit_retry_at`/`usage_limit_episode` durably updated and a `HoldKindLimit` hold projected, preserving the harness's own stated reset time when it supplies one and inventing none when it does not. Reconcile only detects; it never steers a pane, which stays exclusively the watcher's concern, and leaves the backoff itself to whichever mechanism is already managing it.

None of this changes an Attempt's lifecycle, releases a lease, returns a worktree, or touches a Herdr resource. An idle-unreported Attempt is not asserted dead: atqamz/hand#255's own correction established that an idle, still-present harness is exactly the shape a resume can recover, and only `hand reconcile <id> --attempt-never-started`'s existing operator attestation may end an Attempt on the strength of that judgment.

## Rejected alternatives

- Reading each harness's own on-disk session record (Codex's rollout JSONL, Claude's transcript) would be stronger evidence for some facts - a session's own `task_complete` event names its error precisely - but is a materially larger, harness-specific capability of its own, unverified for any harness but the one real Codex session inspected while writing this. Left for separate, evidence-backed follow-up rather than folded in speculatively.
- Auto-terminalizing an idle-unreported Attempt would conflate "never reported" with "never going to", which atqamz/hand#255's correction showed is false for a harness still resident in its pane; only an explicit attestation may draw that conclusion.
- A new schema column for the classification would duplicate `status_changed_for`, which already carries exactly this fact once a watcher writes it; reconcile keeping the same column fresh instead unifies the two paths rather than adding a second notion of liveness.
- Letting reconcile drive the watcher's own backoff/attempt-counting schedule would require reconcile to hold poll-loop state it has no business owning; reconcile detects once per invocation and leaves scheduling to the watcher.

## Consequences

An attempt whose harness died or never began no longer reads as silently healthy forever: `hand reconcile` (and, through the same durable columns, `hand watch`'s catch-up and any later observer) can tell working from idle-unreported without a live watcher ever having run. A quota stop with a stated reset time is preserved the same way whether the watcher or reconcile observed it first. Nothing here shortens the path to ending an Attempt; the existing attestation in atqamz/hand#258 remains the only way to convert "never reported" into a recorded decision.
