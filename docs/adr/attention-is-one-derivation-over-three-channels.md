# Attention is one derivation over three channels

- Date: 2026-08-20
- Status: accepted
- Issues: atqamz/hand#244, atqamz/hand#232, atqamz/hand#53, atqamz/hand#149, atqamz/hand#140, atqamz/hand#32, atqamz/hand#65
- PRs: atqamz/hand#265, atqamz/hand#274

## Context

A run of separate defects in this repository kept resolving to one missing definition.
There is no stated contract for what a report is, what acknowledging one means, or how attention is derived, so each command answers those three questions locally and the answers disagree.

Two decisions already settle pieces of it.
[The report file owns worker outcome](the-report-channel-is-the-only-outcome-signal.md) made `state/<id>.status` the sole source of what a worker says happened, and kept herdr state independent of it.
[Arming a watch observes before it waits](arming-a-watch-observes-before-it-waits.md) made wake level-triggered over durable fleet truth, and closed by naming atqamz/hand#244 as the owner of the acknowledgement model it deliberately did not define.
Neither says how many definitions of attention the tool is allowed to have, nor what distinguishes a condition some watcher announced from one a supervisor has taken responsibility for.

The listed defects, each mapped to the invariant below that would have prevented it.

- atqamz/hand#65, unbounded worker prose filling a supervising session, is invariant 1.
  Prose can be truncated to a summary and a path without losing a fact only because it is a channel of its own; a channel carrying both prose and facts cannot be bounded at all.
- The two occurrences recorded on atqamz/hand#244 itself, where `hand status` printed a `pr:` field parsed out of a worker's `done:` line while `task.pr` was empty and `hand merge` refused with `no PR recorded`, are invariant 1.
  The same fleet state answered both "there is a PR" and "there is no PR" depending on which command was asked.
- The observed task carrying `reported: done`, a merged PR, `flags: merged`, `state: unknown`, and `attempt_lifecycle: running` in one rendered document is invariant 1.
  Two channels disagreed about whether the task was over and the document rendered both without naming the disagreement.
- atqamz/hand#140, a well-formed `paused:` report containing a URL classified as `malformed report` and emitted as a mid-word fragment, is invariant 2.
  A parser verdict decided what the fleet was told, and it failed in the direction that costs most: a healthy worker reporting correctly was surfaced as broken.
- atqamz/hand#149, an in-place report rewrite of exactly the consumed byte length silently skipped, is invariant 3.
  The durable byte offset was the only record of whether a report had reached anyone, and a byte count cannot answer that question about content it no longer covers.
- atqamz/hand#232, `stale` spending the single `--until-event` arm on a task that had already reported done and delivered, is invariant 5.
  The watcher's notion of interesting and the supervisor's notion of needing attention were separate definitions, and the watcher's was derived from elapsed time with no consultation of report state.
- atqamz/hand#53, a generated dashboard that invented a pending decision reading `stopped, reason unknown (idle 27m)` from pane idleness, persisted it, and never revisited it while omitting the real `needs-decision` a worker had written, is invariant 6.
  Its third defect, `unknown` rendered beside a PR URL and a terminal report with nothing reconciling them, is invariant 1 again, and its own suggested direction asked for `unknown` to mean "not recorded" rather than "guessed wrong", which is invariant 7.
- atqamz/hand#32, `hand status` reporting `idle` for a worker parked on an unanswered first-run dialog, is invariant 7.
  Herdr's `agent_status` cannot tell finished from parked, and hand rendered its answer as though it could.

No listed defect exercises invariant 4, because there is nothing yet to mutate.
It is recorded here as a guard on the shape invariant 3 is implemented in, not as a fix for anything observed.

## Decision

Attention is one derivation, and it reads three channels that are stored, rendered, and reasoned about separately.

**Evidence** is what the runtime can check for itself: a process exit, a pane herdr answers for, a pull request GitHub reports merged, a branch that is gone, a lease a worktree pool proves.
**Claims** are what a worker wrote in `state/<id>.status`.
**Acknowledgement** is what a supervisor has taken responsibility for.

Evidence and claims answer different questions and neither substitutes for the other.
Acknowledgement is about neither of them: it is about who has already dealt with what the other two said.

### 1. Factual runtime observation and worker prose are separate channels

Evidence and claims are stored apart, rendered apart, and where they disagree the disagreement is what gets surfaced.
Neither channel is resolved silently in favour of the other, and no field may carry a value from one channel in the shape the other channel's fields use.
A rendered field states which channel it came from wherever both channels can produce one.

Binds `hand status`, `hand watch`, `hand session`, `hand merge`, `hand teardown`, and `hand deliver`.
Claims live in [`internal/state`](../../internal/state/report.go) and nowhere else, because hand only ever reads that file.
Evidence lives in [`internal/herdr`](../../internal/herdr), [`internal/ghutil`](../../internal/ghutil), [`internal/project`](../../internal/project/gaterun.go), [`internal/worktree`](../../internal/worktree), and the durable columns [`internal/store`](../../internal/store) owns.
[`cmd/statusview.go`](../../cmd/statusview.go) and [`internal/watcher`](../../internal/watcher) own the rendering split, and `Event.Verified` is the existing shape of it: a worker's own `done:` announces as `reported-done` until independent evidence makes it `done`.

### 2. Malformed worker prose cannot corrupt durable state

A report line the parser rejects produces a recorded unreadable-report condition and changes nothing else.
No lifecycle transition, no acknowledgement, and no attention flag derives from text the parser rejected.
A line whose state keyword parses is never discarded because its note did not.

Binds `hand status` and `hand watch`.
[`internal/state.ParseReportLine`](../../internal/state/report.go) owns the verdict, `LastReportedState` owns skipping trailing rejected lines rather than letting one erase the last real report, and `internal/watcher.ClassifyReportLine` owns announcing the condition without assigning `LastReportState` from it.
`ReportCursor` carries a content digest alongside its offset so a channel rewritten in place is read whole rather than read as consumed.

### 3. Acknowledgement is explicit and observable

There is a defined act of acknowledging a report, it is recorded durably, and it is visible in command output.
Announcement and acknowledgement are two different facts and the tool spells them differently: announcement says some watcher delivered the condition, acknowledgement says a supervisor owns it now.
Without the second, "needs attention" cannot be told from "needed attention, already handled", which is why the same condition resurfaces every poll.

Binds `hand status`, `hand watch`, and `hand session`, and is recorded by [`internal/store`](../../internal/store).
The two markers that exist today are both announcement markers, not acknowledgement markers: `internal/state.UnacknowledgedTerminalReport` reads the durable report cursor, and `attempt.status_changed_for` names the herdr episode a watcher already announced.
The `unacknowledged` flag `cmd/statusview.go` prints therefore means unannounced, and that spelling is wrong under this record.
The surface acknowledgement is performed through is the operator's to choose and is deliberately not decided here.

### 4. Read-only observation does not silently mutate acknowledgement

Observing is not acknowledging.
`hand status` reads and writes nothing, and no future design may make reading imply acknowledging as a side effect.
If reading should ever acknowledge, that is an explicit flag on an explicit contract, never the default.

Binds `hand status` and every read-only path [`internal/state`](../../internal/state) and [`internal/project`](../../internal/project) expose for it, including `ReadHistoryReadOnly`, `ListReconciliationHistoriesReadOnly`, `ReadHoldReadOnly`, `project.ListReadOnly`, and `runtime.DetectPRReadOnly`.
`hand status` satisfies this today only because there is no acknowledgement record to mutate, so this invariant is a constraint on how invariant 3 is built rather than a description of current behavior.

### 5. `status` and `watch` derive attention from compatible semantics

One definition of what needs attention, consumed by every command that answers the question.
A condition is added to that definition once, and every consumer inherits it.
A consumer may rank, filter, or bound what the definition produces, and may not extend it with a condition of its own.

Binds `hand status`, `hand watch`, `hand session`, and [`internal/notify`](../../internal/notify).
There are three definitions today: `needsAttention` in [`cmd/statusview.go`](../../cmd/statusview.go), `classifyNextAction` in [`cmd/nextaction.go`](../../cmd/nextaction.go), and the `Kind` sets `internal/watcher`'s `NotifyFilter` and `CatchUpFilter` select over.
They already disagree in three observable ways: the watcher has no gate-run kind while `needsAttention` counts `gate-absent` and `gate-unknown`; `KindParked` exists only in the watcher, so `hand status` still prints `state: idle` for exactly the pane atqamz/hand#32 describes; and `CatchUpFilter` drops `stale`, which `needsAttention` never had.
atqamz/hand#234's ranking in `classifyNextAction` is a consumer of the single definition and stays a ranking; atqamz/hand#218's supervisor skill is the other consumer.

### 6. Pane scraping is never primary truth

Pane content is a fallback.
Anything concluded from it is labelled as inferred wherever it is rendered, and is persisted only as mechanism state that the next real observation supersedes, never as a fact about the task.

Binds `hand watch`, `hand spawn`, and `hand promote`, through [`internal/watcher/usagelimit.go`](../../internal/watcher/usagelimit.go) and [`internal/runtime/launch.go`](../../internal/runtime/launch.go), the only two callers of `herdr.Client.PaneRead`.
Primary truth about a pane is `herdr.Client.PaneGet`'s `agent_status`, and `PaneRead` returns a failure as an error rather than as text precisely so a failed read cannot become a finding.
Usage-limit detection is the one place a scrape reaches durable state today, as a `limit` hold and an attempt's usage-limit episode, and it is inside this invariant rather than an exception to it: the hold is a schedule the mechanism clears itself, not a claim about the task.
The tempting fix for atqamz/hand#32's residue is to scrape a stopped pane for first-run prompt signatures the way `confirmLaunch` does, and this invariant is what says that answer must render as inferred and must not be written down as the pane's state.

### 7. Unknown is representable everywhere attention is computed

Where the runtime cannot observe, the recorded and rendered value is unknown.
Unknown is not a guess in either direction, and it never collapses into a definite negative.
"Cannot observe" never renders as "nothing to attend to".

Binds every command that observes: `hand status`, `hand watch`, `hand merge`, `hand teardown`, `hand reconcile`, and `hand session`.
[`ghutil.ObservationState`](../../internal/ghutil/observation.go) is the canonical vocabulary, with `found` and `absent` as the two positive findings and everything that stopped a query from completing as `unknown`, carrying a `Probe` so the unknown travels with the command that asked.
[`project.GateRunObservation`](../../internal/project/gaterun.go) is a second instance of that same vocabulary rather than a separate idea, and [`worktree.LeaseObservation`](../../internal/worktree/worktree.go) is a third with two extra positive findings for the same reason.
A fourth observation reuses `ghutil.ObservationState` and adds a type over it; it does not restate the vocabulary a fourth time.

## Rejected alternatives

- One merged channel, letting the report parser promote prose into the durable fields it names, on the argument that the worker knows what it did.
  The two occurrences recorded on atqamz/hand#244 are the price: a `pr:` field indistinguishable in shape from `project:` and `worktree:`, carrying a URL no runtime had ever observed, with the disagreement surfacing only as a later command failing a precondition.
- Resolving evidence against claims by a fixed rule.
  Evidence-always-wins discards the only channel that can carry `blocked` and `needs-decision`, which no runtime check produces.
  Claims-always-win makes a mistaken completion into fleet truth, which [the report-channel record](the-report-channel-is-the-only-outcome-signal.md) already rejected.
- Letting a read imply acknowledgement, since a supervisor that ran `hand status` has by definition seen the row.
  `hand status` is the most-called command in this tool and is invoked from `hand session` and from unattended paths, so this makes every observation a write, makes a second reader unable to see what the first one saw, and leaves a fleet read that fails halfway having acknowledged an arbitrary prefix of itself.
- Inferring attention per command, on the argument that each command knows its own audience.
  atqamz/hand#232 is what one disagreement between two definitions cost, and the current count is three.
- Classifying worker prose with a model to decide attention.
  Rejected by atqamz/hand#244 as a non-goal and by the report-channel record for the same reason: it turns terminal state into a probability.
- An event bus, a daemon, or a subscription registry to carry acknowledgement between processes.
  [The arming record](arming-a-watch-observes-before-it-waits.md) rejected exactly this for wake, and the argument holds unchanged here: `state/hand.db` and `state/<id>.status` already answer the level question, and acknowledgement is a durable fact rather than a message.
- Treating an unobservable condition as clear, on the argument that a fleet of `unknown` markers is noise.
  That is the false all-clear this tool already refuses in `ghutil`, `project`, `worktree`, and the gate-run check, and atqamz/hand#32 is what it looks like when a definite answer is rendered in unknown's place.
- Seven records, one per invariant.
  atqamz/hand#244's finding is that these are one problem seen seven times, and seven records reproduce the fragmentation that produced the defects, with nothing legitimate to tie them together given this directory keeps no index.
- Co-locating the contract as doc comments beside each implementation, which is this directory's default answer for a behavioral contract.
  No package owns this boundary: it spans `internal/state`, `internal/watcher`, `internal/store`, `internal/herdr`, `internal/ghutil`, `internal/project`, `internal/worktree`, and the `status`, `watch`, `session`, `merge`, and `teardown` commands, and the defects are all disagreements *between* those homes rather than errors inside one.
  A contract written seven times beside seven implementations is the arrangement that let the answers drift.
  The per-invariant behavior stays owned by code, command help, and focused tests as it always was; what lives here is the boundary and the rejected alternatives, which is what this directory's three-part test asks for.

## Consequences

Five follow-on issues implement the rest, one per invariant: atqamz/hand#266 for invariant 1, atqamz/hand#267 for invariant 3 carrying invariant 4 as an acceptance criterion, atqamz/hand#268 for invariant 5, atqamz/hand#269 for invariant 6, and atqamz/hand#270 for invariant 7.
Invariant 2 is met today, by atqamz/hand#140's parse fix and atqamz/hand#149's cursor digest, and gets no follow-on issue.
Invariant 4 is met vacuously and becomes a test obligation on invariant 3's implementation rather than work of its own.
Neither atqamz/hand#252 nor atqamz/hand#240 fully satisfies an invariant on its own: atqamz/hand#252 settles the announcement layer invariant 3 is defined against, and atqamz/hand#240 establishes the vocabulary invariant 7 generalizes.

Announcement and acknowledgement stop being interchangeable words, and one existing output field is misnamed under that split until invariant 3 lands.
`ghutil.ObservationState` becomes the vocabulary a new observation reuses rather than restates, so the count of found/absent/unknown types stops growing with the count of things hand observes.
Any new attention condition is one edit that both `hand status` and `hand watch` inherit, and a consumer that wants less filters rather than redefines.
A disagreement between channels becomes something a supervisor can read directly instead of discovering when a later command refuses a precondition.
