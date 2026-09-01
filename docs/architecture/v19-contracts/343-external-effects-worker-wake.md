---
source_issue: 343
source_title: "docs(architecture): lock external-effect, WorkerWake, idempotency, and reconciliation semantics"
source_url: https://github.com/atqamz/hand/issues/343
source_body_updated_at: 2026-08-29T13:06:15Z
contract_version: v19
---

# Durable external-operation, idempotency, and reconciliation contract

This repository snapshot is the immutable semantic contract captured from #343. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

Normative for #339. #344 owns exact DDL, #345 transaction/currentness composition, #346 adapter capability contracts, #347 read models, and #348 cutover.

Canonical v19 has no semantic `Send`. The exact input/control split is:

```text
semantic instruction
→ immutable WorkerInput in SQLite

provider mechanism needed to make exact executor notice pending input
→ external WorkerWake operation
```

`WorkerWake` is the one canonical name. Do not keep `Send` or `ExecutorSignal` as aliases.

---

# Authority boundary

SQLite is authoritative for Hand's durable semantic intent, operation identity, claims, canonical WorkerInput, and captured evidence. External systems remain authoritative for their own reality:

```text
Git        → repository/worktree reality
filesystem → physical object identity/presence
provider   → actual provider resources/effects
Worker     → WorkerReport claims + explicit WorkerInput acknowledgement evidence through Hand protocol
```

A nil command return, request acceptance, process existence, missing pane/path, timeout, or WorkerReport is never generic success proof.

---

# External operation

One `external_operation` is one durable logical request for one independently decidable external postcondition. Recovery of the same unresolved request reuses the same operation identity; a genuinely new semantic request gets a new identity.

The replacement #344 DDL must republish the exact full operation-kind set. The mandatory vocabulary change is:

```text
REMOVE: send
ADD:    worker-wake
```

Native worktree/session/launch/interrupt/residual/qualification/integration/production/publication operation families remain subject to their existing typed contracts after re-audit.

There is no generic `result`, `delivery`, `partial`, or provider-shaped operation state.

---

# Six operation states

Exactly:

```text
prepared
submitted
succeeded
rejected
no-effect
uncertain
```

Transitions:

```text
prepared  → submitted | succeeded | no-effect
submitted → succeeded | rejected | no-effect | uncertain
uncertain → succeeded | rejected | no-effect
```

Meanings:

- `prepared`: complete typed request durable; mutation boundary not durably authorized.
- `submitted`: durable authorization committed immediately before first external mutation; effect may have happened.
- `succeeded`: exact desired capability-specific postcondition positively established.
- `rejected`: provider definitively refused the exact submitted request.
- `no-effect`: positive evidence proves desired effect did not occur and no safety-relevant residual remains unresolved.
- `uncertain`: mutation was authorized but strongest evidence cannot classify succeeded/rejected/no-effect.

Timeout/unavailable/unknown never becomes `no-effect` by absence of evidence.

---

# Write-ahead protocol

## Tx A — prepare

```text
BEGIN IMMEDIATE
revalidate exact semantic/resource parents
insert external_operation(prepared)
insert exactly one matching typed child
insert required secondary scope claims
COMMIT
```

Observe before mutation where useful. A `prepared` operation may settle directly only when positive evidence already proves its exact postcondition or exact no-effect condition without crossing the mutation boundary.

## Tx B — submit

Immediately before the first external mutation:

```text
BEGIN IMMEDIATE
revalidate exact operation = prepared
revalidate typed child + parents + claims
prepared → submitted
COMMIT
```

Only after commit may external mutation begin. Never hold a SQLite write transaction across provider/Git/filesystem mutation.

## Tx C — classify

Observe strongest exact external evidence, then in one writer transaction revalidate the same operation/currentness and apply at most one legal transition plus exact typed evidence/binding updates.

Crash after Tx B is therefore durably distinguishable from “never authorized”.

---

# Operation identity / scope claims

Every operation has immutable exact identity, kind, adapter, request digest, owner/currentness relations, operation key, primary exclusive scope, and typed request child.

`request_digest` covers canonical serialization of all request fields that can change external behavior.

Secondary claims are durable SQLite coordination, e.g. exact workspace/worktree/integration/executor-control scopes. They are acquired only while the owner operation is `prepared`, remain while unresolved, and release only after terminal evidence makes release safe.

Path/display labels never substitute for exact resource identity. Filesystem aliasing may require an additional cross-process physical serialization/observation protocol under #345/#346; SQLite textual claims do not pretend to prove physical identity.

---

# Safe retry / reconciliation

A submitted/uncertain mutating call may be repeated automatically only when #346 positively guarantees provider-enforced idempotency for the same persisted operation key.

Otherwise:

```text
restart / timeout / lost process memory
→ observe exact external target first
→ reconcile same operation
→ never blind retry
```

A replacement operation may be created only after the predecessor is terminal and the domain/resource currentness still permits a new request.

Operation history is not resource history. For example, one still-current Attempt may have terminal nonsuccess WorktreeCreate O1 followed by successful O2 without fabricating a new semantic Attempt.

---

# WorkerInput is not an external effect

Creating semantic input is canonical SQLite work under #345/#344:

```text
exact current Attempt + ExecutorBinding
→ insert immutable ordered WorkerInput
→ COMMIT
```

That commit proves only that Hand durably recorded the instruction for the exact execution generation.

It does not prove:

```text
WorkerWake happened
provider accepted wake
Worker observed input
Worker acted on input
WorkerReport/outcome
```

If crash occurs after WorkerInput commit but before wake preparation/submission, the semantic instruction is still safe and durable. Recovery may create/reconcile a WorkerWake without duplicating WorkerInput.

---

# WorkerWake

`worker-wake` is a narrow provider-facing mechanism operation whose desired postcondition is **mechanism-level**, not semantic instruction delivery.

Typed request names the exact:

```text
Attempt
SessionBinding
ExecutorBinding
operation key
bounded wake reason / pending-input boundary required for reconciliation
```

It carries no arbitrary operator/Answer prose as semantic authority.

A wake may coalesce multiple pending WorkerInputs. The Worker drain path consumes all eligible input for the exact ExecutorBinding in canonical ordinal order. Wake race/completion order never changes semantic input order.

Provider acceptance/triggering can be the strongest mechanism postcondition only when the adapter contract says so; it is still not WorkerInputAcknowledgement.

## Multi-step wake residual

If Herdr/provider wake requires staged terminal bytes + Enter:

- stage only a constant/bounded Hand-owned doorbell, never WorkerInput payload bytes;
- Tx B occurs before first staged external mutation;
- represent safety-relevant staged residual through exact typed provider evidence;
- unresolved residual blocks competing executor-control mutation where required;
- residual cleanup is its own exact operation under shared executor-control scope;
- cleanup never rewrites whether WorkerInput exists or whether Worker acknowledged it.

This makes an interactive provider menu unable to reinterpret arbitrary operator semantic bytes as a menu selection.

---

# WorkerInputAcknowledgement

Acknowledgement is immutable evidence naming one exact WorkerInput. It proves only that the Worker observed/drained that exact input under the protocol.

It is not an external-operation success state and never means instruction obeyed, Attempt completed, Decision semantically resolved, or Task/Plan progressed.

No read/render/orient/wake/provider-acceptance path may create acknowledgement implicitly.

---

# Native Git WorktreeCreate / WorktreeRemove

Retain the exact native Git safety contract:

```text
adapter = builtin/git-worktree
path is locator, not ownership
positive common-dir/private-gitdir/lock-reason/HEAD/physical-identity proof
submitted before mutation
unknown ownership/registration/preservation → unresolved + Attention/Repair
```

WorktreeCreate success is positive registered-resource proof, not `git` exit 0. A positively no-effect predecessor may be followed by another create on the same still-current Attempt.

Before WorktreeRemove freshly re-prove exact binding ownership, physical identity, Git registration/admin identity, no open dependent SessionBinding, cleanliness where relevant, and preservation safety. Clean alone is not preservation-safe. Routine cleanup does not use broad `--force`/prune as authority.

---

# Session / Launch / Interrupt

SessionBinding is exact Worker Attempt provider addressability, not Supervisor Harness runtime identity.

Session acquire/release settles only from exact positive provider observation.

Launch commits `submitted` before first provider execution mutation and succeeds only after exact ExecutorBinding establishment is positively proven. Ambiguous launch creates no fabricated executor/Attempt terminality.

Interrupt succeeds only from positive exact executor cessation, never request acceptance.

WorkerWake, Interrupt, and provider residual cleanup use shared executor-control exclusion when their mechanisms could interfere.

---

# Qualification / Integration / Production / Publication

Preserve separation between external operation success and semantic verdict/result evidence:

```text
qualification operation succeeded
!= qualification verdict qualified
```

Integration/Production/Publication remain exact typed operations over exact revisions/artifacts/targets. No generic Result/Delivery persistence.

---

# Reconciliation invariant

For every unresolved operation:

```text
load exact operation + typed child + claims
observe exact external subject outside SQLite writer
classify strongest positive evidence
BEGIN IMMEDIATE
revalidate exact operation/current parents/claims
apply one legal transition/evidence update
COMMIT
```

Predecessor evidence settles predecessor only. Unknown never clears ownership, Repair, or scope claims. Cleanup always names exact resource identity and revalidates current external ownership before mutation.

---

# Required crash/race tests

At minimum cover:

- crash after prepare/before submission;
- crash after submission/before mutation call;
- crash during/after mutation before local classification;
- remote/Git success before local receipt;
- uncertain then later positive reconciliation;
- provider unavailable during reconciliation;
- WorktreeCreate submitted then positive no-effect then replacement create;
- stale cleanup after same path/resource is reallocated to a new exact owner;
- WorkerInput commit then crash before WorkerWake;
- WorkerWake accepted then crash before WorkerInputAcknowledgement;
- multiple concurrent inputs + coalesced/racing wakes preserve ordinal order;
- unresolved WorkerWake residual vs Interrupt/next wake exclusion;
- interactive provider menu cannot consume semantic WorkerInput bytes because wake carries only bounded doorbell;
- stale Attempt/Executor wake/input never retargets successor execution.

---

# Lock conditions

- `WorkerInput` is canonical semantic input, never external `Send`.
- `WorkerWake` is the only canonical wake noun/operation kind.
- Six external-operation states only.
- Submission is durably committed before first external mutation.
- Unknown/timeout/restart never authorizes blind retry or destructive cleanup.
- Exact resource identity/currentness is revalidated at every mutation/reconciliation boundary.
- Provider acceptance never becomes WorkerInput acknowledgement/semantic success.
- #344 must publish or immutably reference replacement exact DDL matching this contract before #339 implementation.