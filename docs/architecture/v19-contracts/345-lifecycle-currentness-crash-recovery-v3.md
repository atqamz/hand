---
source_issue: 345
source_title: "docs(architecture): lock lifecycle, WorkerInput currentness, concurrency, and crash recovery"
source_url: https://github.com/atqamz/hand/issues/345
contract_version: v19-lifecycle-v3
supersedes_path: docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v2.md
supersedes_blob: 50d6747ae140e68faddf15ed3d8337bfa85596c9
---

# Lifecycle, transaction, currentness, concurrency, crash-recovery, and Task archive contract — revision 3 candidate

This candidate supersedes revision 2 only for the evidence that the Task archive writer requires. It does not modify revision-1 or revision-2 bytes.

Revision 2 continues to govern everything else: every lifecycle, WorkerInput, effect, resource, concurrency, and crash-recovery rule, and the rest of "Task archive currentness". That rest includes the exact relation, the WorkerReport archive boundary, WorkerInput non-fabrication, the SQLite writer race, and exact replay. The #344 DDL does not change.

This revision becomes normative only after an independently reviewed permanent manifest anchors this exact Git blob. Until then revision 2 remains the anchored snapshot.

## Decision

Operator decision, 2026-09-25: a terminal Task whose lineage held external resources may be archived once every WorktreeBinding, SessionBinding, and ExecutorBinding has its durable release or termination fact and every external operation is terminal. Archive requires no fresh external observation.

## Superseded revision-2 clauses

Revision 3 replaces exactly these revision-2 clauses:

1. In the archive writer transaction, the step `establish fresh positive external resource observations`, and the words `and observation identities` in the step `revalidate exact Task identity and observation identities`.
2. In "Task archive currentness", the paragraph that begins "Positive database predicates do not prove Git/filesystem/provider state." It is replaced in full by "Archive evidence" below. Revision-2 rules that require fresh external identity before an external mutation, such as "Resource cleanup always re-proves exact current external identity before destruction", are unchanged.
3. The acceptance line "Archive re-proves the full relational predicate in one `BEGIN IMMEDIATE` transaction and requires positive external observations." It is replaced by the acceptance lines below.

## Archive writer

The archive writer uses one transaction:

```text
BEGIN IMMEDIATE
revalidate exact Task identity
require Task lifecycle satisfied|superseded|abandoned
require no active Plan or Attempt in the exact Task lineage
require no unresolved prepared|submitted|uncertain external operation
require no open direct or inbound TaskHold
require no unresolved AttemptBackoff
require no open WorktreeBinding, SessionBinding, or ExecutorBinding
require no unacknowledged WorkerReport in exact handling-worthy state paused|blocked|needs-decision|done|failed
require no open Decision
require no open Repair targeting the Project/Task lineage or its exact resources/effects
insert exactly one TaskArchive fact
COMMIT
```

No step reads Git, the filesystem, a provider, or a process. The writer establishes no external observation before or inside the transaction.

The resource predicates are exact #344 relations:

- An external operation is in the exact Task lineage when its `task_id` is the Task. The #344 `external_operation_insert_guard` requires every Plan- or Attempt-scoped operation to carry its exact Task, so this set is complete.
- An external operation is resolved when its state is `succeeded`, `rejected`, or `no-effect`. `prepared`, `submitted`, and `uncertain` are unresolved.
- A WorktreeBinding is open until its `worktree_binding_release` row exists.
- A SessionBinding is open until its `session_binding_release` row exists.
- An ExecutorBinding is open until its `executor_binding_termination` row exists.

The #344 `task_archive_insert_guard` trigger enforces each of these predicates and remains the final guard. A writer may check them earlier for a clearer refusal, but no writer check replaces the trigger.

## Archive evidence

Durable facts are sufficient for archive for these reasons:

- A canonical writer records each release or termination fact at its own mutation boundary, from #346 evidence. #344 `worktree_binding_release_guard` requires the exact `succeeded` WorktreeRemove, and #346 WorktreeRemove requires fresh exact ownership, registration, and physical identity. #344 `session_binding_release_guard` requires the exact `succeeded` SessionRelease, and #346 requires positive provider postcondition evidence for it. #346 revision 2 records `executor_binding_termination` only from an EG-8 termination source.
- Not every such fact is an observation. #346 revision 2 EG-8 admits operator-attested settlement as a termination source, and EG-16 states that attestation is not observation. By the operator decision, attested facts satisfy the archive predicate. These include an attested `provider-gone` termination from `exec-guard-lost` or from the claimed `exec-guard-launch-uncertain` branch, and an attested settlement of an `uncertain` WorkerWake or Launch to `succeeded` or `no-effect`. #344 has no provenance guard on `executor_binding_termination`, so the predicate does not distinguish an attested row from an observed one.
- Each fact that archive relies on stays true. #344 permits no transition out of `succeeded`, `rejected`, or `no-effect`, and it rejects UPDATE and DELETE of release and termination rows. Later evidence may still append after archive, including a new external operation for the archived Task. Revision 2 and #347 revision 3 keep that evidence visible without deleting the archive fact or reopening lifecycle.
- Archive is projection classification, not teardown or cleanup. It authorizes no external mutation, so it needs no fresh external identity.

The archive `evidence_digest` remains the bounded actor-supplied digest that the #344 relation records. Revision 3 does not require it to bind an external observation.

## Residuals

- Archive trusts a release or termination fact recorded by Hand as Hand's own durable evidence. Archive does not re-prove that fact against Git, the filesystem, a provider, or a process.
- Archive trusts operator attestation wherever #346 admits it as a termination source or an operation settlement. This includes the descendant that #346 revision 2 says the Linux scan cannot see: a process that re-executed with a cleaned environment and left the terminal and the worktree.
- Archive trusts that every `succeeded` WorktreeCreate, SessionAcquire, and Launch wrote its binding in the same transaction. The canonical writers do this, but #344 does not enforce that a `succeeded` operation has its binding. A `succeeded` operation without its binding leaves no binding for the archive predicate to check.
- An observed executor termination proves that the guarded process tree T(G) ceased. It does not prove that every effect the harness caused has stopped, as #346 revision 2 states for work started through an outside daemon.
- An out-of-band change after release does not block archive and does not change durable history. Examples are a directory re-created at a released Worktree path, or a provider session started under a released key. Such a resource is not held through the archived lineage. If a later canonical writer records evidence about it, such as a Repair, #347 Attention surfaces that evidence as revision 2 requires.
- A release fact that Hand recorded wrongly is not detected by archive. After archive the default active view omits the Task, and exact detail and history still show every binding, release, termination, and operation.
- Archive never deletes or rewrites history. It inserts one `task_archive` row and changes no lifecycle, binding, release, termination, operation, report, input, or acknowledgement row.

## Falsifiable invariants

- **AR-1** An archive request for a Task with an external operation in `prepared`, `submitted`, or `uncertain` refuses, and no `task_archive` row exists afterwards.
- **AR-2** An archive request for a Task with an `attempt_worktree_binding` that has no `worktree_binding_release` refuses, and no `task_archive` row exists afterwards.
- **AR-3** An archive request for a Task with a `session_binding` that has no `session_binding_release` refuses, and no `task_archive` row exists afterwards.
- **AR-4** An archive request for a Task with an `executor_binding` that has no `executor_binding_termination` refuses, and no `task_archive` row exists afterwards.
- **AR-5** A direct `task_archive` INSERT that bypasses the writer refuses in each case of AR-1 to AR-4.
- **AR-6** A terminal Task whose operations are all resolved and whose bindings all have their release or termination fact archives when no other revision-2 predicate blocks it. The archive succeeds with Git, the filesystem, and every provider unavailable to the writer.
- **AR-7** A successful archive inserts exactly one `task_archive` row and leaves every other row unchanged.
- **AR-8** When evidence for an archived Task appends after archive commits, such as a new external operation, WorkerReport, Decision, or Repair, the `task_archive` row is unchanged, the Task stays terminal, and #347 Attention and history show the new evidence.

## Acceptance / lock conditions

These lines replace the superseded revision-2 acceptance line. Every other revision-2 acceptance line stays in force.

- [ ] Archive re-proves the full relational predicate in one `BEGIN IMMEDIATE` transaction, including resolved operations and release or termination facts, and requires no fresh external observation.
- [ ] Each of an unresolved operation, an open WorktreeBinding, an open SessionBinding, and an open ExecutorBinding refuses archive with no archive fact.
- [ ] A fully released terminal lineage archives without Git, filesystem, or provider access.
- [ ] Archive inserts exactly one fact and rewrites no history.

DDL impact on #344: NONE.
