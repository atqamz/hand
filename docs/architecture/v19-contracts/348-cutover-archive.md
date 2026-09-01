---
source_issue: 348
source_title: "docs(architecture): lock v18→v19 cutover, WorkerInput non-fabrication, and legacy archival semantics"
source_url: https://github.com/atqamz/hand/issues/348
source_body_updated_at: 2026-08-29T13:06:19Z
contract_version: v19
---

# Canonical persistence cutover and legacy archival contract

This repository snapshot is the immutable semantic contract captured from #348. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

Normative for #339. Legacy canonical source accepted for automatic cutover is the supported v18 Fleet DB; target is the **replacement v19 schema that #344 must relock**.

**#348 must not pin the withdrawn pre-WorkerInput #344 DDL/hash/fingerprint.** The current target identifiers are whatever exact replacement payload #344 publishes or immutably references at relock time.

Cutover is side-by-side fresh canonical build + immutable legacy archive. It imports only facts v18 can positively prove and fabricates no Task/Plan/Attempt/resource/input/effect history from ambiguous legacy evidence.

---

# Authority rule

At startup, database validity outranks marker prose and user-local registry projection.

A valid canonical v19 active DB means at least:

```text
state/hand.db opens read-only
user_version = 19
exact current #344 schema fingerprint/object contract matches
integrity_check = ok
foreign_key_check = empty
Fleet singleton/identity structurally valid
```

When true, active canonical DB is authority even if cutover marker is stale/missing or registry projection is degraded.

Never overwrite a valid canonical DB from marker/archive.

A corrupt/structurally invalid v19 DB is not silently replaced from legacy archive because it may contain newer canonical work. Fail closed for explicit recovery.

---

# Supported automatic source

Automatic cutover accepts exactly the shipped/supported v18 source contract, including exact schema/layout fingerprint and safe rollback-journal state.

Reject older/newer/unknown/mismatched/recovery-needing/WAL-active sources before source mutation.

Do not run an old migration ladder merely to manufacture v18 semantics for import.

Read-only surfaces (`status`, Presentation, `hand orient`, `hand session start`, etc.) never perform cutover, schema migration, WAL checkpoint, marker writes, or registry repair. They report typed legacy/recovery-required state.

---

# Inter-process / SQLite source safety

Cutover uses one cross-process Fleet MigrationLock. Under it, reopen/re-read source and establish one consistent read transaction using read-only/query-only SQLite with foreign keys enabled.

Refuse when source safety cannot be established, including unsupported journal mode, active WAL/SHM, unresolved journal, integrity/FK failure, or inability to exclude supported Hand writers.

Never open source read-write, checkpoint, VACUUM, rollback, or mutate merely to make migration easier.

---

# Quiescence / refusal

Canonical v19 cannot truthfully import unresolved active v18 runtime state.

Refuse automatic cutover while any safety-relevant legacy fact is live/nonterminal/ambiguous, including:

```text
open/nonterminal Task or execution
unresolved launch/executor terminality
unresolved Treehouse lease/worktree ownership/release
unresolved Herdr session/executor ownership/termination
pending/ambiguous legacy semantic Send or staged terminal input residual
open safety Repair/Hold/Backoff controlling future work
unresolved external effect
repository/workspace identity/revision ambiguity
```

Provider unavailable is `unknown`, never permission to forget state.

Legacy runtime must reconcile/terminalize first; then cutover may retry.

---

# Migration identity / archive

Derive deterministic migration identity from exact Fleet identity + exact source DB digest under a documented versioned algorithm.

Use deterministic same-volume paths for:

```text
active state/hand.db
canonical temp sibling
cutover marker
immutable archive directory
original legacy hand.db archive
manifest
allowed semantic sidecars
```

Never overwrite an existing archive identity unless exact digests prove it is the same evidence.

Archive preserves forensic truth, not current authority.

Manifest records exact source/archive digests, Fleet ID, v18 version, target v19 contract identity from the **current relocked #344 payload**, imported Project/workspace evidence, observed physical identities/revisions, and sidecar digests.

Exclude repositories, credentials, tokens, caches, binaries, private-runtime bundles, and unrelated files.

---

# Import classification

Every legacy fact is exactly one of:

```text
provably importable
derivable-and-positively-verified-at-cutover
archive-only
blocking/ambiguous
discarded mechanism cache
```

Nothing becomes canonical merely because a legacy column/file exists.

## Fleet

Preserve exact durable Fleet identity. Never remint because registry/path differs.

## Projects / WorkspaceBinding / PolicyRevision

Import only positively established current Project repository binding/policy facts that can be normalized into canonical v19 without inventing history.

For each imported Project positively verify:

```text
canonical Fleet-relative repository locator
exact Git repository
physical filesystem identity
exact commit revision
no locator/physical alias with another imported Project
```

Legacy display name/URL/mode/upstream are provenance/policy inputs only, not Project identity.

Do not invent ProjectAcquisition/Relocation effects Hand did not perform.

## Task / Plan / Attempt / runtime history

Terminal v18 execution history is archive-only unless an exact target relation is independently provable under this spec.

Do **not** synthesize canonical:

```text
Task
Plan
Attempt
WorktreeBinding
SessionBinding
ExecutorBinding
TerminalReceipt
WorkerReport / WorkerReportAcknowledgement
WorkerInput / WorkerInputAcknowledgement
WorkerWake
Interrupt
Qualification / Integration
Production / Artifact / Publication
```

from terminal legacy task/runtime/prose/cursor history.

If operator wants an old goal pursued again after cutover, create a new canonical Task.

---

# WorkerInput non-fabrication rule

Legacy v18 terminal text/report/pane/status/Send history does not prove the exact canonical tuple required for WorkerInput:

```text
WorkerInput identity
exact target Attempt
exact target ExecutorBinding
per-executor ordinal
semantic origin
exact acknowledgement evidence
```

Therefore:

```text
legacy prose / pane / send-like state
→ archive under normal legacy evidence rules where required
→ create ZERO fabricated WorkerInput rows
→ create ZERO fabricated WorkerInputAcknowledgement rows
→ create ZERO fabricated WorkerWake operations
```

If unresolved staged input/semantic Send residual or a live exact legacy executor cannot be reconciled positively, cutover is blocked rather than translating ambiguity into new canonical input/wake history.

This is mandatory even if a legacy field happens to contain the exact same payload bytes.

---

# Legacy Treehouse rule

Treehouse lease/pool identity is never translated into native v19 WorktreeBinding.

```text
terminal/quiescent legacy Treehouse evidence
→ archive-only

unresolved Treehouse ownership/release
→ blocks cutover
```

Fresh v19 WorktreeBinding requires its own native Git operation identity, binding ID, exact registered/common-dir/private-gitdir/lock-reason/HEAD/physical identity evidence and cannot be reverse-invented from Treehouse history.

---

# Fresh canonical temp DB

Build a new deterministic temp DB only after source/archive/import evidence is established.

Conceptually:

1. create empty temp DB;
2. execute **exact current replacement #344 DDL** from the exact authority #344 identifies;
3. insert exact Fleet singleton/identity;
4. insert migration/import evidence with source/archive/manifest digests;
5. insert only allowed Project/WorkspaceBinding/PolicyRevision/import/archive facts;
6. create zero fabricated canonical execution/input/effect history;
7. set `PRAGMA user_version=19` only after target construction;
8. require exact current #344 fingerprint/object contract;
9. require FK check empty and integrity `ok`;
10. close/flush temp before publication.

No provider/Git/registry/network mutation occurs between final temp validation and canonical DB publication except the exact filesystem publication primitives.

---

# Marker is advisory only

Cutover marker records deterministic recovery evidence/phase, expected source/archive/temp/target contract identities, and paths.

Marker is never authority. Corrupt/missing/stale marker cannot override a valid canonical DB or digest-validated archive/temp evidence.

---

# Publication / durability

Keep publication same-volume and crash-safe.

POSIX: fsync completed files before rename and fsync relevant parent directories after publication boundaries.

Windows: close handles, flush completed files, use same-volume write-through rename/move semantics, and treat sharing violations as bounded stop/retry—not copy-over/delete fallback.

Do not pretend Windows and POSIX durability primitives are identical.

Publication sequence conceptually:

```text
validate+flush archive evidence + canonical temp
→ persist advisory prepared marker
→ atomically archive original active v18 DB
→ reopen/hash/validate final archive
→ ensure archive evidence exactly matches temp import plan
→ atomically publish canonical temp as state/hand.db
→ reopen active v19 read-only and validate exact current #344 contract/FK/integrity/Fleet/import evidence
→ canonical DB becomes authority
→ registry projection reconciles separately/idempotently
```

If final archived source digest differs from snapshot used to build temp, do not publish stale temp.

Archive contains the original legacy DB file, not only a logical export.

---

# Startup recovery matrix

Required deterministic outcomes:

```text
valid canonical v19 active
→ canonical wins; repair marker/registry only as projections

valid recognized v18 active
→ legacy wins; resume only exact matching cutover evidence under MigrationLock

active missing + valid archive + matching valid canonical temp
→ publish exact temp after full validation

active missing + valid archive + temp absent/invalid
→ rebuild fresh temp from exact archive evidence then publish

corrupt/invalid canonical v19 active
→ fail closed; no automatic archive restore/down-conversion

unknown/newer schema
→ refuse before mutation

marker conflicts with files
→ cryptographic/schema evidence outranks marker; if unique authority cannot be proven, refuse
```

No mtime-based authority selection.

---

# Registry projection boundary

User-local `registry.db` remains discovery/projection only.

After canonical identity is positively known:

- preserve canonical `fleet_id` regardless of stale locator claims;
- reconcile/retire only provably stale same-home locator projections through supported registry operations;
- unrelated Fleet ambiguity cannot make this Fleet's DB identity ambiguous;
- projection failure after successful canonical publication leaves canonical DB authoritative and reports degraded/repair-required state;
- test/disposable cutover must use isolated Secondhand infrastructure root and never mutate operator production registry.

Incidents #357–#360 are regression inputs to this boundary, not justification for a registry table inside Fleet DB.

---

# Required crash/adversarial tests

At minimum inject:

- every marker/archive/temp/publication rename/flush boundary;
- two concurrent migration processes;
- source changes between initial read snapshot and final archive publication;
- unexpected destination exists;
- Windows sharing violations;
- POSIX rename/dir-fsync crash windows;
- corrupt temp and corrupt active canonical DB;
- WAL/SHM/journal refusal;
- Project locator/physical alias;
- unresolved Treehouse resource;
- unresolved legacy Send/staged input residual;
- exact zero fabricated WorkerInput/Acknowledgement/WorkerWake rows;
- valid canonical DB + stale old registry locator preserves canonical Fleet ID;
- valid Fleet A + unrelated ambiguous Fleet B does not block A;
- test cutover leaves real user registry unchanged.

Every case proves one unique authority and no silent deletion/overwrite/fabrication.

---

# Acceptance / lock conditions

- [ ] Target schema identity comes only from the replacement relocked #344 payload; withdrawn old hash/fingerprint is not implementation authority.
- [ ] Valid canonical v19 DB outranks marker/archive/registry projection.
- [ ] Automatic source acceptance is exact, read-only, quiescent, and fail-closed.
- [ ] Original v18 DB is preserved as immutable archive evidence.
- [ ] Only positively provable Fleet/Project/workspace/policy facts are imported.
- [ ] Terminal legacy execution/resource/report/send history is archive-only unless independently proven importable.
- [ ] ZERO fabricated WorkerInput/WorkerInputAcknowledgement/WorkerWake from legacy prose/send-like state.
- [ ] Legacy Treehouse identity is never mapped into native WorktreeBinding.
- [ ] Unresolved Treehouse/Herdr/input/effect ownership blocks cutover.
- [ ] Publication/recovery is crash-safe on POSIX and native Windows.
- [ ] Registry remains projection; stale same-home locator never remints Fleet identity.
- [ ] Test cutover cannot reach operator production registry.
- [ ] #344 replacement DDL/fingerprint/query proof is validated before v19 publication.

No automatic downgrade is provided once a valid v19 Fleet exists.