---
source_issue: 348
source_title: "docs(architecture): lock v18→v19 cutover, WorkerInput non-fabrication, and legacy archival semantics"
source_url: https://github.com/atqamz/hand/issues/348
contract_version: v19-cutover-v2
supersedes_revision: 6699745bb451e3daca072b4c958e5b79f31e4775
supersedes_path: docs/architecture/v19-contracts/348-cutover-archive.md
supersedes_blob: c5c8045698ca8bea3b093d1adbe13d0b87c651d0
writer_exclusion_issue: 524
mechanical_probe_pr: 525
---

# Canonical persistence cutover and legacy archival contract — revision 2

This repository snapshot supersedes the original #348 cutover snapshot for current v19 implementation authority. The original snapshot remains immutable historical evidence. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

This revision changes only the source-writer exclusion, original-archive ordering, frozen-bridge recovery, and publication ordering needed to close #524. It does **not** change canonical v19 relational semantics or any byte of #344.

Normative for #339. Legacy canonical source accepted for a fresh automatic cutover is the exact supported v0.7.2 Fleet DB contract. Target remains the exact replacement v19 schema owned by #344.

Cutover is side-by-side fresh canonical build + immutable original legacy archive. It imports only facts v0.7.2 can positively prove and fabricates no Task/Plan/Attempt/resource/input/effect history from ambiguous legacy evidence.

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

Never overwrite a valid canonical DB from marker/archive/frozen-bridge evidence.

A corrupt/structurally invalid v19 DB is not silently replaced from legacy archive because it may contain newer canonical work. Fail closed for explicit recovery.

---

# Supported automatic source

Fresh automatic cutover accepts exactly the shipped/supported v0.7.2 source contract, including exact schema/layout identity and safe rollback-journal state.

Reject older/newer/unknown/mismatched/recovery-needing/WAL-active sources before the deliberate freeze boundary.

Do not run an old migration ladder merely to manufacture supported source semantics for import.

An exact frozen bridge defined below is **not** a supported fresh legacy source and is never classified as ordinary v0.7.2. It is recognized only by dedicated cutover recovery when its exact bridge certificate/guards and matching immutable original archive prove that a previous cutover already crossed the one-way freeze boundary.

Read-only surfaces (`status`, Presentation, `hand orient`, `hand session start`, etc.) never perform cutover, schema migration, WAL checkpoint, freeze writes, marker writes, or registry repair. They report typed legacy/frozen/recovery-required state.

---

# Inter-process / SQLite source safety

Cutover uses one cross-process Fleet MigrationLock to serialize cutover processes. MigrationLock alone is **not** old-writer exclusion because ordinary v0.7.2 writers do not hold it.

Under MigrationLock, final source inspection uses exactly one pinned SQLite connection opened write-capable solely because SQLite writer reservation itself requires write capability.

The ordering is mandatory:

```text
open pinned mode=rw connection with foreign_keys=ON
→ BEGIN IMMEDIATE
→ require writer reservation success
→ PRAGMA query_only=1
→ verify query_only=1
→ perform final source reads/checks/archive only
```

Do not set `query_only=1` before `BEGIN IMMEDIATE`: mechanical proof #525 shows that ordering makes the reservation fail with `SQLITE_READONLY` on the production driver.

`BEGIN IMMEDIATE` is the writer gate. If another supported writer already holds a conflicting write reservation/transaction, acquisition fails/blocks according to the bounded busy policy and cutover stops/retries. Once the reservation is held, no second supported writer can acquire a successful write reservation before the gate transaction ends.

While `query_only=1`, source mutation is forbidden. Final exact family/layout validation, journal/WAL/SHM checks, integrity/FK checks, quiescence source reads, import-plan reads, Fleet identity, and exact source digest/archive bytes are established only after the writer reservation exists.

Refuse when safety cannot be established, including unsupported journal mode, active WAL/SHM, unresolved journal, integrity/FK failure, inability to acquire the writer gate, or any quiescence ambiguity.

Never checkpoint, VACUUM, run the old migration ladder, or mutate data merely to make cutover easier.

---

# Exact original archive before freeze

The forensic original archive is created while the writer gate is held and `query_only=1`.

Required ordering:

```text
writer gate held
+ query_only=1
+ exact source validated/quiescent
→ read exact active DB bytes
→ derive source SHA-256 and migration identity
→ write immutable-archive temp on the same volume
→ flush file
→ atomically publish archive path
→ flush relevant parent directory
→ reopen archive and require exact digest equality
```

No source DB mutation may occur before this archive is durable and revalidated.

Migration identity remains a documented versioned derivation from exact Fleet identity + exact **original source DB digest**. The later frozen bridge has a different digest and never replaces the original digest in migration identity or forensic provenance.

Never overwrite an existing archive identity unless exact digests prove it is the same evidence.

Archive preserves forensic truth, not current authority.

---

# One-way frozen bridge

After the exact original archive is durable and revalidated, the same pinned `BEGIN IMMEDIATE` transaction may cross one explicit freeze boundary.

The only permitted source mutations at this boundary are the exact frozen-bridge mechanism below. First set `PRAGMA query_only=0` on the same pinned reserved connection and verify it is disabled. Then, atomically in that already-reserved transaction:

1. require `meta.key = 'v19-cutover-freeze'` to be absent;
2. insert exactly one freeze certificate:

```text
key   = v19-cutover-freeze
value = v1:<lowercase 64-hex SHA-256 of exact original source DB bytes>
```

3. create exactly 21 unconditional write guards: `BEFORE INSERT`, `BEFORE UPDATE`, and `BEFORE DELETE` for each exact supported legacy table:

```text
task
attempt
fleet_identity
meta
hold
project
send_attempt
```

4. use trigger names exactly:

```text
v19_freeze_<table>_INSERT
v19_freeze_<table>_UPDATE
v19_freeze_<table>_DELETE
```

5. every guard aborts with exactly:

```text
legacy source frozen for v19 cutover
```

6. set `PRAGMA user_version = 22` as the frozen-bridge sentinel;
7. `COMMIT` the freeze transaction.

`user_version=22` here is a cutover mechanism sentinel, not a new canonical schema version and not a supported legacy schema. Canonical v19 remains `user_version=19`.

If freeze commit cannot complete, roll back and stop/retry. A failed/rolled-back freeze must leave the active source as exact supported v0.7.2 semantics; the already durable original archive may remain as non-authoritative evidence.

After freeze commit:

- a fresh v0.7.2 `Open` must fail closed as newer-than-supported before baseline schema work;
- a stale already-open/prepared v0.7.2 writer must fail every canonical table DML through the persistent guards;
- writer exclusion no longer depends on keeping the SQLite reservation handle open;
- the original immutable archive remains byte-identical to the pre-freeze source.

The frozen bridge is mechanism/recovery state only. It is never imported as legacy semantic authority and never replaces the original archive.

Dedicated recovery recognizes a frozen bridge only when all of these hold:

```text
user_version = 22
exact supported v0.7.2 base table/index semantics
exactly the 21 required freeze triggers and no unexpected trigger semantics
exact freeze-certificate key/value format
certificate source digest matches an immutable original archive
Fleet identity from the bridge agrees with the archived source/import evidence
```

A `user_version=22` database that does not match the exact bridge contract is unknown/newer schema and fails closed.

A frozen bridge without a matching immutable original archive fails closed. Never reconstruct or bless an original archive from the already-mutated bridge.

---

# Quiescence / refusal

Canonical v19 cannot truthfully import unresolved active legacy runtime state.

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

# Migration identity / archive contents

Use deterministic same-volume paths for:

```text
active state/hand.db
canonical temp sibling
cutover marker
immutable archive directory
original legacy hand.db archive
optional frozen-bridge recovery/retired path
manifest
allowed semantic sidecars
```

Manifest records exact original source/archive digests, Fleet ID, supported source version/layout identity, freeze-certificate version/digest, target v19 contract identity from the current #344 authority, imported Project/workspace evidence, observed physical identities/revisions, and sidecar digests.

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

The freeze certificate/triggers/sentinel are `discarded mechanism cache`; they are not imported domain facts.

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

Terminal legacy execution history is archive-only unless an exact target relation is independently provable under this spec.

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

Legacy terminal text/report/pane/status/Send history does not prove the exact canonical tuple required for WorkerInput:

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

Build a new deterministic temp DB from the immutable original archive/import plan after successful freeze, or during recovery from that same immutable archive evidence.

Conceptually:

1. create empty temp DB;
2. execute exact current #344 DDL from the authority #344 identifies;
3. insert exact Fleet singleton/identity;
4. insert migration/import evidence with original source/archive/manifest digests;
5. insert only allowed Project/WorkspaceBinding/PolicyRevision/import/archive facts;
6. create zero fabricated canonical execution/input/effect history;
7. set `PRAGMA user_version=19` only after target construction;
8. require exact current #344 fingerprint/object contract;
9. require FK check empty and integrity `ok`;
10. close/flush temp before publication.

No frozen-bridge trigger/certificate/sentinel row is copied into canonical v19.

No provider/Git/registry/network mutation occurs between final temp validation and canonical DB publication except the exact filesystem publication primitives.

---

# Marker is advisory only

Cutover marker records deterministic recovery evidence/phase, expected original source/archive/frozen-bridge/temp/target contract identities, and paths.

Marker is never authority. Corrupt/missing/stale marker cannot override a valid canonical DB or cryptographically/schema-validated archive/frozen-bridge/temp evidence.

A crash after freeze commit must remain recoverable even if the marker update did not land: exact frozen-bridge certificate + exact matching immutable original archive provide the durable binding. If that unique binding cannot be proven, fail closed.

---

# Publication / durability

Keep publication same-volume and crash-safe.

POSIX: fsync completed files before rename and fsync relevant parent directories after publication boundaries.

Windows: close cutover-owned handles before filesystem publication, flush completed files, use same-volume write-through rename/move semantics, and treat sharing violations from stale external handles as bounded stop/retry—not copy-over/delete fallback.

Do not pretend Windows and POSIX durability primitives are identical.

Publication sequence conceptually:

```text
MigrationLock
→ BEGIN IMMEDIATE writer gate
→ query_only=1
→ final source safety/quiescence/import read plan
→ publish+flush+reopen/hash exact immutable original archive
→ persist advisory prepared/original-archived marker
→ query_only=0 at explicit freeze boundary
→ write exact freeze certificate + 21 guards + user_version=22
→ COMMIT freeze
→ close cutover-owned source handles
→ build/validate/flush canonical temp from exact original archive/import plan
→ retire/move frozen bridge away from active state/hand.db
→ atomically publish canonical temp as state/hand.db
→ reopen active v19 read-only and validate exact current #344 contract/FK/integrity/Fleet/import evidence
→ canonical DB becomes authority
→ registry projection reconciles separately/idempotently
```

If a Windows sharing violation prevents moving the already-frozen bridge, stop/retry. Do not unfreeze it and do not copy-over/delete around the sharing violation. The bridge guards preserve writer exclusion while recovery waits.

If the immutable original archive digest ever differs from the source digest recorded in the freeze certificate/import plan, do not publish canonical temp.

Archive contains the original pre-freeze legacy DB file bytes. A retired frozen bridge may be retained as bounded mechanism/recovery evidence but must be labeled distinctly and never presented as the original archive.

---

# Startup recovery matrix

Required deterministic outcomes:

```text
valid canonical v19 active
→ canonical wins; repair marker/registry only as projections

valid exact supported v0.7.2 active
→ legacy wins; resume/restart cutover only under MigrationLock and a fresh writer gate

exact frozen bridge active + exact matching immutable original archive
→ original archive is legacy semantic/import evidence; resume cutover from archive; never import bridge mechanism rows/triggers

frozen bridge active + archive absent/mismatched/ambiguous
→ fail closed; never reconstruct original evidence from bridge

active missing + valid original archive + matching valid canonical temp
→ publish exact temp after full validation

active missing + valid original archive + temp absent/invalid
→ rebuild fresh temp from exact archive evidence then publish

corrupt/invalid canonical v19 active
→ fail closed; no automatic archive restore/down-conversion

unknown/newer schema other than the exact frozen bridge contract
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

At minimum inject/prove:

- `query_only=1` before `BEGIN IMMEDIATE` is rejected and never used as the gate ordering;
- `BEGIN IMMEDIATE → query_only=1` acquires writer exclusion without changing source bytes;
- a conflicting existing writer prevents writer-gate acquisition;
- a second/new writer cannot acquire a successful write reservation while the gate is held;
- source digest/original archive are established only after writer-gate acquisition;
- `query_only=0` is crossed only after exact original archive durability/revalidation;
- freeze certificate + all 21 guards + sentinel commit atomically;
- a prepared stale pre-freeze writer cannot mutate any guarded table after freeze;
- a fresh v0.7.2 open refuses the frozen sentinel;
- failed freeze commit rolls back to supported source semantics;
- immutable original archive bytes remain unchanged after freeze;
- every marker/archive/freeze/temp/publication rename/flush boundary;
- crash after original archive but before freeze;
- crash after freeze commit before marker/temp/publication;
- exact frozen bridge + exact archive recovery;
- frozen bridge with missing/mismatched archive refusal;
- two concurrent migration processes;
- unexpected destination exists;
- Windows sharing violations leave the frozen bridge safe and retryable;
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

- [ ] Target schema identity comes only from the current #344 immutable DDL/proof authority.
- [ ] Valid canonical v19 DB outranks marker/archive/frozen-bridge/registry projection.
- [ ] Fresh automatic source acceptance is exact, quiescent, and fail-closed.
- [ ] Writer exclusion uses `BEGIN IMMEDIATE` before query-only source inspection; MigrationLock is not misrepresented as an old-writer rendezvous.
- [ ] Source mutation is zero until the exact original archive is durable/revalidated; after that only the exact one-way frozen-bridge mechanism is permitted before canonical publication.
- [ ] Original pre-freeze DB bytes are preserved as immutable archive evidence and remain the migration-identity digest authority.
- [ ] Exact frozen bridge blocks stale and new supported old writers and is recoverable only with matching immutable original archive evidence.
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

DDL impact on canonical v19/#344: **NONE**.