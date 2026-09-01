---
source_issue: 345
source_title: "docs(architecture): lock lifecycle, WorkerInput currentness, concurrency, and crash recovery"
source_url: https://github.com/atqamz/hand/issues/345
source_body_updated_at: 2026-08-29T13:06:16Z
contract_version: v19
---

# Lifecycle, transaction, currentness, concurrency, and crash-recovery contract

This repository snapshot is the immutable semantic contract captured from #345. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

Normative for #339. #344 owns exact relational DDL; #343 owns external-effect/WorkerWake semantics; #346 owns adapter/runtime observation contracts; #347 owns read models; #348 owns cutover.

Canonical durable hierarchy remains:

```text
Fleet → Project → Task → Plan → Attempt
```

Canonical semantic input is `WorkerInput`. Semantic `Send` currentness/race semantics are not part of v19.

---

# Non-negotiable currentness rules

1. SQLite is canonical semantic currentness authority. Process mutexes, PID/ancestry, watcher/supervisor ownership, browser state, Git/filesystem locks, paths, and provider sessions are evidence/coordination only.
2. Writers that choose current rows, allocate ordinals, create/supersede current children, create WorkerInput, prepare effects, open/close episodes, or accept exact terminal evidence use `BEGIN IMMEDIATE`.
3. Currentness is exact relational identity + lifecycle/open predicates + lineage + ordinals + exact resource/evidence IDs. No generic `state_version`, conversation epoch, or current-task row.
4. New per-parent ordinal allocation occurs inside the same writer transaction as insertion; relational UNIQUE is final arbiter.
5. Semantic updates name the exact old/current predicate and require exactly one row to change. Zero means stale/conflict; never retarget “whatever is current now”.
6. Revision-bearing canonical insertion positively verifies the exact Git commit in the exact Project repository and revalidates DB currentness before commit.
7. No external mutation occurs inside a SQLite transaction or before #343 durable submission authorization.
8. SQLite busy retry retries only the DB transaction, never an unresolved provider/Git effect.
9. External locks may serialize physical primitives SQLite cannot lock, but DB witnesses are revalidated after lock acquisition.
10. Read/status/FleetSnapshot/Attention/`hand orient` are mutation-free.
11. Supervisor runtime ownership/wake coordination never authorizes workflow writes.
12. Stale UI/direct actions and stale Supervisor actions lose through the same writer predicates.

---

# Task / Plan / Attempt lifecycle

Task:

```text
active → satisfied | superseded | abandoned
```

Plan:

```text
active → satisfied | superseded | abandoned
```

Attempt:

```text
active → completed | failed | interrupted
```

No terminal reopen.

`retry` creates a new Attempt under the same immutable Plan. `replan` supersedes the exact active Plan and creates a successor Plan. `advance` leaves the satisfied predecessor satisfied and creates a successor with explicit lineage.

Plan meaning—intent/judgment/basis/brief/captured WorkspaceBinding/PolicyRevision—is immutable. Attempt freezes resolved Worker Harness/Profile/model/effort/session-adapter provenance.

No stale historical Attempt evidence can satisfy or mutate successor work.

---

# TaskHold

`TaskHold` is immutable Task-level deferral evidence. It is distinct from Decision and from AttemptBackoff.

Baseline kinds are exactly:

```text
operator
blocked
```

Executor/provider-specific limit conditions are not Task-level hold truth; they are represented as AttemptBackoff when they satisfy that contract.

Optional relationships are typed, exact relations rather than overloaded nullable ownership fields:

```text
TaskHold
├─ optional blocked-on Task relation
├─ optional Decision relation when genuine Task deferral and Decision coexist
└─ optional bounded recheck-not-before relation
```

Closure is a separate immutable one-to-one `TaskHoldResolution` with exactly:

```text
released | cancelled | superseded
```

Open hold is derived by absence of `TaskHoldResolution`. Do not persist `Task.held`, mutable hold state, or a current-hold pointer.

---

# AttemptBackoff

`AttemptBackoff` is one immutable delay/re-observation episode for one exact active Attempt. It is not TaskHold and not semantic Retry.

Baseline reasons are exactly:

```text
usage-limit
rate-limit
provider-transient
```

Each episode records the exact Attempt, per-Attempt ordinal, `not_before`, bounded evidence digest, and creation evidence. At most one unresolved AttemptBackoff may exist for one Attempt.

Closure is a separate immutable one-to-one `AttemptBackoffResolution` with exactly:

```text
resumed | cancelled | superseded
```

`now >= not_before` grants only eligibility to re-observe or apply the owning recovery rule. Elapsed time alone never authorizes Worker steering, Retry, lifecycle progression, or provider mutation. Resume/re-observation revalidates this issue's exact currentness.

An Attempt must not terminalize while an unresolved AttemptBackoff exists. Resolve or supersede that Backoff in the same canonical writer transaction before terminalization.

---

# Repair

`Repair` is immutable diagnosis/reconciliation evidence. It is not mutable lifecycle state on Task, Plan, Attempt, or any resource.

A Repair has:

```text
repair identity
stable bounded machine repair_code
bounded reason/evidence digest
created_at
exactly one typed canonical target
```

The target is a mechanically FK-backed typed sum, not `owner_kind + owner_id` polymorphic text and not nullable path/provider guesses. Baseline target families are:

```text
Project
WorkspaceBinding
Task
Plan
Attempt
external_operation
WorktreeBinding
SessionBinding
ExecutorBinding
```

#344 owns the exact typed-target relational shape and must enforce exactly one target per Repair.

`repair_code` is a stable machine token. Every code emitted by implementation must have a real supported recovery path; provider-shaped prose is not relational ontology.

Closure is a separate immutable one-to-one `RepairResolution` with exactly:

```text
repaired
no-longer-applicable
superseded
operator-attested
```

Resolution carries exact bounded evidence. Open Repair is derived by absence of `RepairResolution`.

`operator-attested` is valid only through the owning resource-specific authority contract. It is scoped authority, not a generic force-clear escape hatch.

---

# Shared Hold / Backoff / Repair invariants

- Insert and resolution writers use `BEGIN IMMEDIATE` plus exact owner/currentness predicates.
- Evidence and resolution rows are append-only immutable history.
- Stale Hold/Backoff/Repair evidence never retargets successor Plan/Attempt/resource identities.
- Open state is derived relationally from evidence minus resolution, never mutable pending/current flags.
- Presentation, FleetSnapshot, Attention, and SupervisorOrientation may project openness but never own it.

---

# WorkerInput creation/currentness

Creating `WorkerInput` is a canonical semantic write, not provider delivery.

Use one writer transaction requiring the exact current execution generation:

```text
BEGIN IMMEDIATE
require exact Task/Plan/Attempt lineage
require Plan active
require Attempt active
require exact open SessionBinding
require exact established/open ExecutorBinding
require WorkerInput identity unused
allocate next ordinal for exact ExecutorBinding
insert immutable WorkerInput
COMMIT
```

The input binds permanently to that exact Attempt/ExecutorBinding.

Required race outcomes:

```text
input for A1/E1 vs retry/replacement A2/E2
→ whichever exact semantic commit is valid first wins
→ stale input writer fails
→ never retarget to A2/E2

same WorkerInput identity vs replay
→ one canonical identity; duplicate replay converges/refuses idempotently

same payload + different identity
→ legal new instruction, ordered after predecessor

concurrent distinct inputs for E1
→ unique deterministic ordinals
→ no external wake race may reorder them
```

If the target later terminalizes, unacknowledged input remains exact historical evidence; it never migrates to a successor.

---

# WorkerInputAcknowledgement currentness

Acknowledgement is an immutable child of one exact WorkerInput. It proves only that Worker execution observed/drained that input according to the protocol.

It may be appended only by the exact acknowledgement writer and exact execution evidence rules chosen in #344/#346.

It never proves:

```text
instruction obeyed
semantic success
Attempt completion
Decision downstream effect
Plan/Task progression
WorkerWake success
```

No read/render/orient/status/wake/provider-acceptance/WorkerReport path creates acknowledgement implicitly.

Historical acknowledgement, if valid under the exact contract, remains attached to historical input; it never acknowledges a successor input.

---

# WorkerWake composition

After WorkerInput commits, a provider `WorkerWake` may be needed:

```text
WorkerInput durable
→ #343 prepare exact WorkerWake
→ durable submitted before first provider mutation
→ perform/observe/reconcile mechanism
```

Crash between input commit and wake loses no semantic instruction. Recovery sees the same pending input and may create/reconcile wake without inserting a replacement WorkerInput.

Crash after provider accepts wake but before WorkerInputAcknowledgement leaves input unacknowledged. Acceptance != acknowledgement.

The Worker drains all eligible inputs for its exact ExecutorBinding in ordinal order. A wake may coalesce many pending inputs.

If WorkerWake, Interrupt, or provider residual cleanup can interfere, they contend under the same exact executor-control operation scope. External control serialization never becomes semantic currentness authority.

---

# Plan immutability versus steering

WorkerInput may clarify or steer execution only within the immutable meaning of the active Plan.

If operator/Supervisor intent materially replaces delegated objective/basis/meaning, do not hide a replan inside WorkerInput. Create the appropriate successor Plan/Attempt first, then target any new input to that exact successor execution.

---

# Native Git WorktreeBinding currentness

Canonical v19 uses exact native Git linked WorktreeBinding; no generic Isolation provider or Treehouse lease identity exists in fresh v19.

Worktree path is locator, not authority.

WorktreeCreate/Remove require exact DB ownership plus fresh Git/filesystem observation at the mutation boundary. Success/cleanup relies on exact common repository, private gitdir, lock reason, HEAD/basis, physical filesystem identity, and preservation conditions required by #343/#346.

Required properties:

- at most one unresolved WorktreeCreate per Attempt;
- terminal nonsuccess/no-effect create may be followed by a new create on the same still-current Attempt;
- successful WorktreeBinding prevents later creates;
- open SessionBinding blocks WorktreeRemove;
- stale cleanup from old Attempt/operation cannot remove same path reallocated to a different resource identity;
- unknown ownership/registration/preservation blocks destruction;
- clean worktree alone is not preservation proof;
- routine broad `--force`/prune is not ownership authority.

External filesystem/Git serialization aids races but never replaces exact relational witnesses.

---

# Session / Launch / Executor currentness

SessionBinding is exact Attempt-scoped Worker provider addressability, never Supervisor Harness runtime identity.

Launch prerequisites require exact active Attempt + exact open WorktreeBinding + exact open SessionBinding. Launch submits before first provider execution mutation and creates ExecutorBinding only from positive exact establishment evidence.

Ambiguous launch remains unresolved and creates no fabricated ExecutorBinding/Attempt terminality.

One SessionBinding is not silently reused to invent replacement executor identity.

Interrupt terminalizes only from positive exact executor cessation evidence.

---

# WorkerReport / Decision / authority separation

WorkerReport is immutable Attempt-scoped Claim. WorkerReport acknowledgement names one exact report and is distinct from WorkerInputAcknowledgement.

Decision/Answer authority remains #304-owned.

```text
Decision answered
→ may create exact Answer-origin WorkerInput
→ may require WorkerWake
→ may later receive WorkerInputAcknowledgement
```

None of those later stages retroactively change what “Decision answered” means.

---

# External-operation composition / recovery

Every provider/Git/filesystem effect follows #343 prepare → observe → submitted-before-mutation → exact classify/reconcile.

Restart/timeout/process loss never grants automatic replay unless #346 proves provider-enforced idempotency for the same operation key.

Reconciliation revalidates exact operation/current parents/claims before committing any resolution. Evidence about predecessor settles predecessor only.

---

# Required race table

At minimum prove:

| race | required result |
| --- | --- |
| retry vs retry | one active Attempt/ordinal wins; loser stale |
| retry vs replan | one semantic commit wins; loser never retargets |
| replan vs replan | exact predecessor/UNIQUE permits one successor |
| Task supersede vs supersede | one exact successor |
| TaskHold insert/resolve vs successor/currentness change | exact old owner predicate wins or stale writer fails; never retarget |
| AttemptBackoff insert/resolve vs terminalization | terminalization requires unresolved Backoff resolved/superseded first |
| Repair resolution vs target replacement | exact target/currentness wins or stale resolution fails; never retarget |
| concurrent WorkerInput | unique exact per-executor ordinals |
| same WorkerInput identity replay | one canonical identity |
| WorkerInput vs Attempt/Executor replacement | stale writer fails, never retargets |
| WorkerInput acknowledgement vs terminalization | exact historical attachment only; never retarget |
| WorkerWake vs Interrupt/residual cleanup | exact shared executor-control scope |
| two WorktreeCreates same Attempt | one unresolved; later fresh op only after predecessor terminal nonsuccess |
| WorktreeRemove vs SessionAcquire | open dependency/currentness prevents unsafe removal |
| stale cleanup vs reallocated same path | exact identity mismatch blocks mutation |
| stale Supervisor vs stale direct UI | same domain writer rejects both |

---

# Supervisor runtime coordination

Supervisor runtime/session ownership may fence duplicate reasoning/wake consumption operationally, but never workflow writes.

Canonical counterexample must remain safe:

```text
S1 orient → sees generation G1
S2 orient → sees G1
S1 commits exact G1 transition
S2 submits stale action derived from G1
→ exact writer predicate fails stale
→ never retarget successor G2
```

Watcher alive, bridge attached, wake accepted, Supervisor re-entry, and `hand orient` completion are distinct operational facts and none substitutes for #345 relational currentness.

---

# Crash-recovery invariants

- SQLite committed history is recovery authority, process memory is not.
- A crash after #343 `submitted` is not distinguishable as “call never happened”; observe/reconcile first.
- A crash after WorkerInput commit cannot lose semantic input.
- A crash after wake acceptance cannot fabricate input acknowledgement.
- Resource cleanup always re-proves exact current external identity before destruction.
- Unknown stays unresolved/actionable.
- Open Hold/Backoff/Repair state is reconstructed from immutable evidence/resolution relations, not mutable convenience flags.
- Read models remain bounded and mutation-free during recovery/orientation.

---

# Acceptance / lock conditions

- [ ] Exact relational predicates, not session/process/paths, own currentness.
- [ ] `BEGIN IMMEDIATE` is used for current-child/ordinal/WorkerInput/effect/lifecycle/Hold/Backoff/Repair writers.
- [ ] TaskHold is immutable Task deferral evidence, distinct from Decision/AttemptBackoff, with separate immutable resolution and no mutable Task hold shortcut.
- [ ] AttemptBackoff is exact Attempt-scoped delay/re-observation evidence, distinct from Retry, with at most one unresolved episode and no time-based mutation authority.
- [ ] An Attempt cannot terminalize with unresolved AttemptBackoff.
- [ ] Repair is immutable reconciliation evidence with exactly one typed canonical target and separate immutable resolution; `operator-attested` is scoped, not generic force-clear authority.
- [ ] Hold/Backoff/Repair openness is derived relationally and stale evidence never retargets successors.
- [ ] WorkerInput binds exact active Attempt + ExecutorBinding and never retargets.
- [ ] Per-executor WorkerInput ordinal allocation is deterministic/concurrency-safe.
- [ ] WorkerInputAcknowledgement is immutable exact evidence distinct from WorkerReport acknowledgement and action/outcome.
- [ ] WorkerWake is mechanism only and cannot reorder semantic inputs.
- [ ] Plan meaning cannot be conversationally replaced through WorkerInput.
- [ ] Native Worktree cleanup revalidates exact identity/preservation and blocks stale reallocation teardown.
- [ ] External effects obey #343 durable submission/reconciliation rules.
- [ ] Supervisor/watcher/runtime coordination never replaces writer currentness.
- [ ] #344 replacement DDL mechanically supports every relation/index/constraint needed above before #339 implementation.