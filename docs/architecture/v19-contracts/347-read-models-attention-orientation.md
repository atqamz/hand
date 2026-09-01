---
source_issue: 347
source_title: "docs(architecture): lock WorkerInput-aware read models, Attention, and supervisor orientation"
source_url: https://github.com/atqamz/hand/issues/347
source_body_updated_at: 2026-08-29T13:06:18Z
contract_version: v19
---

# Canonical read model, WorkerReport replay, WorkerInput visibility, Attention, and SupervisorOrientation

This repository snapshot is the immutable semantic contract captured from #347. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

Normative for #339. Read models are disposable, deterministic projections over #344 canonical rows plus explicitly timestamped #346 observations. They never become a second workflow database and never authorize mutation.

Canonical v19 has no semantic `Send` read model. WorkerInput, WorkerInputAcknowledgement, and WorkerWake remain distinct facts.

---

# Truth families stay separate

```text
Git/filesystem/provider Observation
!= WorkerReport Claim
!= WorkerReport Acknowledgement
!= WorkerInput
!= WorkerInputAcknowledgement
!= WorkerWake mechanism state
!= semantic lifecycle/effect truth
!= operator authority
```

Examples:

- Worker `done` is a Claim, not Attempt/Plan/Task completion;
- WorkerInput durable does not mean Worker observed it;
- WorkerWake accepted does not mean WorkerInput acknowledged;
- WorkerInputAcknowledgement does not mean the instruction was obeyed;
- reading/rendering never acknowledges anything;
- cleanup does not prove executor terminality.

---

# WorkerReport ingestion / replay

Preserve the exact WorkerReport contract:

```text
<state>: <note>
```

with exact semantic states:

```text
working | paused | blocked | needs-decision | done | failed
```

Complete-record identity remains based on exact source-prefix evidence, not cursor authority:

```text
(attempt_id, source_prefix_digest)
```

`source_end_offset` is evidence only. Acknowledgement names one exact immutable WorkerReport ID.

Correctness requires full-source replay capability after checkpoint loss/contradiction, but steady-state ingestion may use a disposable positively verified prefix checkpoint. That checkpoint is mechanism state only and may never become report/currentness/acknowledgement authority.

`hand orient` must not rescan arbitrarily large historical report files every turn. Recovery replay remains available when checkpoint proof fails.

---

# WorkerInput read model

FleetSnapshot/SupervisorOrientation may expose bounded **current** WorkerInput facts derived from immutable canonical rows:

```text
worker_input_id
exact attempt_id
exact executor_binding_id
ordinal
origin_kind / bounded origin reference
created_at
acknowledged   // DERIVED from exact WorkerInputAcknowledgement existence
age / bounded summary
```

Do not persist or project mutable authority such as:

```text
pending=true
handled=true
input_read
last_input_cursor
last_worker_input_seen
current_input
needs_wake
```

Unacknowledged/pending is relationally derived from:

```text
exact WorkerInput
MINUS exact WorkerInputAcknowledgement
PLUS exact target/currentness facts
```

Historical input for a superseded/terminal execution remains historical evidence. It must not manufacture current Attention merely because it is unacknowledged.

Required distinction:

```text
current WorkerInput unacknowledged
!= WorkerWake unresolved/degraded
!= executor dead
!= Worker ignored instruction
!= instruction failed
```

Surface contradictions/unknowns explicitly rather than collapsing them into one generic `pending`/`stalled` state.

---

# WorkerInputAcknowledgement

WorkerInputAcknowledgement is a distinct relation from WorkerReport Acknowledgement.

Only the exact WorkerInput acknowledgement writer may append it.

None of these acknowledge WorkerInput:

```text
FleetSnapshot/status rendering
hand orient
WorkerWake requested/submitted/accepted
executor state change
WorkerReport arrival
Decision answered/resolved
notification delivery
```

Acknowledgement proves only exact Worker observation/drain under the protocol, not action/compliance/outcome.

---

# WorkerWake / executor-control projection

Read models expose unresolved/recent WorkerWake mechanism state separately from semantic input:

```text
exact worker-wake operation identity
exact Attempt / SessionBinding / ExecutorBinding
prepared|submitted|succeeded|rejected|no-effect|uncertain
bounded mechanism evidence/diagnostic
residual observation where provider mechanics can leave one
```

A provider wake may coalesce many WorkerInputs. No read model derives semantic input order from wake order.

If provider wake uses staged terminal material, only the bounded Hand-owned doorbell/residual mechanism is represented. Semantic WorkerInput payload bytes never become terminal residual authority.

WorkerWake/Interrupt/residual-cleanup safety obligations remain exact external-operation facts under #343.

---

# Hold / Backoff / Repair projection

TaskHold, AttemptBackoff, and Repair semantics are owned by #345; exact relations are owned by #344.

Read models derive openness only from immutable evidence minus the corresponding immutable resolution relation. They never persist mutable `held`, `backoff_active`, `repair_pending`, current-pointer, or equivalent authority.

A historical unresolved-looking row that is no longer a current actionable obligation must not be retargeted to successor work. Genuine unresolved historical safety obligations remain visible explicitly.

---

# FleetSnapshot

Conceptual projection:

```text
FleetSnapshot
  fleet_id
  db_read_at
  projects[]
    Project
    current WorkspaceBinding + physical identity observation
    current PolicyRevision
    bounded Git/workspace observations
    active Tasks[]
      exact Task + lineage leaf
      active/satisfied Plan facts
      latest/active Attempt
      WorktreeBinding / SessionBinding / ExecutorBinding evidence
      latest bounded WorkerReports + exact report acknowledgements
      bounded current WorkerInputs + derived input acknowledgements
      unresolved WorkerWake / Interrupt / resource/effect operations
      accepted TerminalReceipt / Qualification / Integration facts
      open Holds / Backoffs / Repairs
      exact available actions + opaque currentness
  unresolved_historical_obligations[]
  counts
  explicit bounds/continuations
```

Every active Task is represented. Historical detail may be bounded, but unresolved safety obligations are never history-bounded away.

An old terminal Attempt with unresolved WorktreeRemove, SessionRelease, WorkerWake residual, uncertain operation, or Repair remains visible after successor work begins.

---

# Snapshot timing / read-only semantics

SQLite facts come from one consistent read transaction tagged `db_read_at`.

External Git/filesystem/provider observations occur outside that DB snapshot and carry independent `observed_at` plus `found|absent|unknown` and exact ownership/diagnostic evidence where needed.

The combined projection is never claimed atomically observed. #345 writer revalidation remains mandatory.

FleetSnapshot/Attention/orient construction never:

```text
acknowledges WorkerReport
acknowledges WorkerInput
creates WorkerInput
creates/retries WorkerWake
settles operations
retries/replans/advances
answers Decision
repairs/cleans resources
claims Supervisor workflow ownership
performs migration/cutover
```

---

# Set-oriented hot query plan

Primary snapshot/orientation must use a bounded number of indexed set queries, not SQL/process calls per Task.

At minimum the #344 replacement DDL/query-plan proof must support:

1. Fleet + Projects + current WorkspaceBinding/current PolicyRevision;
2. active Tasks and exact lineage leaves;
3. active/satisfied Plans + latest/active Attempts;
4. open WorktreeBinding/SessionBinding/ExecutorBinding;
5. TerminalReceipt/Qualification/Integration summaries;
6. latest bounded WorkerReports + exact report acknowledgements;
7. current WorkerInputs for current ExecutorBindings ordered by ordinal;
8. unacknowledged WorkerInputs via exact acknowledgement anti-join/existence;
9. unresolved/latest WorkerWake and other external operations + scope claims;
10. open Holds/Backoffs/Repairs/Decisions;
11. bounded requested history with continuation.

`hand orient` must not scan all historical WorkerInput/ack or obligation rows every turn.

External observations should be grouped by strongest actual identity (e.g. repository/common-dir, provider/session grouping) rather than N+1 per rendered Task.

---

# Attention is pure derivation

There is no canonical Attention table, mutable inbox, persisted next-action queue, or `unannounced` flag.

Conceptual item:

```text
AttentionItem
  priority
  code
  exact project/task/plan/attempt IDs where applicable
  exact evidence_id or deterministic exact composite
  bounded reason
  observation refs/timestamps where relevant
  available action kinds
  opaque exact-currentness witness per action
```

Stable identity is exact evidence/currentness, never prose, timestamps, browser/Supervisor session, pane labels, report offsets, or wake count.

Priority classes remain conceptually:

| Priority | Class |
| ---: | --- |
| 10 | safety uncertainty / unresolved external effect / destructive-ownership ambiguity |
| 20 | explicit operator decision / current handling-worthy exact evidence |
| 30 | semantic progression |
| 40 | safe retry/resume |
| 50 | exact historical cleanup |

Equal-priority order is deterministic by exact canonical identities.

---

# WorkerInput Attention rules

A **current** WorkerInput that remains unacknowledged beyond an owning bounded policy/mechanism condition may contribute Attention.

Keep separate Attention/evidence for:

```text
current semantic input unacknowledged
WorkerWake path degraded/unresolved
executor observation unknown/dead
provider residual blocking next control
```

Never infer “Worker ignored instruction” or “instruction failed” from missing acknowledgement alone.

Historical unacknowledged input must not remain current Attention after its exact Attempt/Executor is superseded/terminal except where an explicit historical safety/audit obligation genuinely remains.

---

# Worktree / resource safety Attention

Native Git WorktreeBinding remains canonical v19 Attempt resource identity.

Priority safety Attention includes unresolved create/remove, exact path/common-dir/private-gitdir/lock-reason/HEAD/physical-identity contradiction, alias/replacement ambiguity, open Session dependency, uncommitted/preservation-unsafe cleanup, and unknown destructive ownership.

Path missing or clean worktree alone is never cleanup permission.

Legacy Treehouse may appear only as explicit v18 reconciliation/cutover Attention under #348, never fresh v19 identity.

---

# SupervisorOrientation

SupervisorOrientation is a bounded Hand-owned projection for Fleet-level reasoning:

```text
FleetSnapshot + Attention + bounded runtime diagnostics
→ hand orient
→ Supervisor Harness
→ reason
→ exact canonical operation
→ #345 writer revalidates
```

It owns no conversation history, current-task memory, acknowledgement cursor, provider lifecycle, or authorization.

Deleting/replacing Supervisor runtime loses zero canonical truth.

Canonical Supervisor lifecycle:

```text
new Supervisor Harness runtime/session
→ hand session start once
→ runtime bootstrap / wake-delivery integration only

every reasoning turn
→ hand orient
→ fresh SupervisorOrientation
```

Keep operational health dimensions separate where surfaced:

```text
runtime identity/addressability
bootstrap/integration readiness
watcher liveness
wake-delivery attachment
wake-delivery progress
orientation/reasoning progress
```

A live process/lock/watcher is not proof of progress or delivery health.

---

# Watcher semantics

Watcher is level/current-state hint infrastructure:

```text
ingest newly observable report material using verified mechanism checkpoint
read canonical DB
observe required external sources
derive current Attention/monitor predicates
if level true → wake
else arm
on event/timeout/restart → repeat from authority
```

Events/wakes are hints. Missed/duplicate/stale events and checkpoint loss cannot alter semantic truth.

A Supervisor wake always leads to fresh `hand orient` before reasoning/action.

---

# Presentation / Interaction

Status/board/TUI/GUI/mobile/notifications consume the same projection families and own no workflow truth.

Already-exact typed actions may call canonical Hand services directly; reasoning-required input flows through Supervisor Harness. Both rely on exact #345 currentness.

Presentation refresh is mutation-free.

---

# Acceptance / rejection criteria

- [ ] WorkerReport full replay remains correctness fallback while steady-state ingestion is bounded/incremental only under positive checkpoint proof.
- [ ] WorkerReport acknowledgement and WorkerInputAcknowledgement remain distinct exact immutable evidence.
- [ ] Current WorkerInput visibility is derived from exact rows/ack existence, never mutable pending/cursor state.
- [ ] Historical unacknowledged input cannot manufacture current successor Attention.
- [ ] WorkerWake state/residual is separate from WorkerInput semantic state.
- [ ] Provider wake acceptance never becomes input acknowledgement.
- [ ] Hold/Backoff/Repair openness is derived relationally, never mutable read-model authority.
- [ ] FleetSnapshot represents all active Tasks and unresolved safety obligations.
- [ ] Snapshot/orient DB work is set-oriented and indexed, including current/unacknowledged WorkerInput and open-obligation hot paths.
- [ ] `hand orient` remains bounded and does not scan full report/input/obligation history by design.
- [ ] Attention remains pure deterministic derivation with exact identity/currentness.
- [ ] Native Worktree identity/preservation ambiguity remains safety Attention.
- [ ] Supervisor runtime liveness/attachment/progress dimensions remain separate.
- [ ] Rendering performs no acknowledgement/mutation/effect settlement.
- [ ] No generic Result/Delivery/Isolation/persisted current-task/pending-input authority appears.
- [ ] #344 replacement DDL/query-plan proof supports these hot paths before #339 starts.