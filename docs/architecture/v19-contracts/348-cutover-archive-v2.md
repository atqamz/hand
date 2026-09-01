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
mechanical_probe_head: 5f985170f6ece5d60fc9f083bfef70b0d8bbc667
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

Fresh automatic cutover accepts exactly the shipped/supported v0.7.2 source contract, including exact schema/layout identity and a mechanically safe rollback-journal state.

Reject older/newer/unknown/mismatched/WAL-active sources before the deliberate freeze boundary. A rollback journal is not rejected merely because a `-journal` pathname exists: existence alone does not prove a hot or unsafe journal. Conversely, an unresolved recovery state is never assumed safe.

A rollback-journal residue/race is admissible only through the SHARED→PENDING→EXCLUSIVE protocol below and only when the committed snapshot, candidate archive, and post-EXCLUSIVE active DB all revalidate to the same exact DB digest and semantic source state. Any recovery that changes the active DB bytes or semantic source state from the candidate blocks automatic cutover.

Do not run an old migration ladder merely to manufacture supported source semantics for import.

An exact frozen bridge defined below is **not** a supported fresh legacy source and is never classified as ordinary v0.7.2. It is recognized only by dedicated cutover recovery when its exact bridge certificate/guards and matching immutable original archive prove that a previous cutover already crossed the one-way freeze boundary.

Read-only surfaces (`status`, Presentation, `hand orient`, `hand session start`, etc.) never perform cutover, schema migration, WAL checkpoint, freeze writes, marker writes, or registry repair. They report typed legacy/frozen/recovery-required state.

---

# Inter-process / SQLite source safety

Cutover uses one cross-process Fleet MigrationLock to serialize cutover processes. MigrationLock alone is **not** old-writer exclusion because ordinary v0.7.2 writers do not hold it.

`BEGIN IMMEDIATE` is also insufficient as the final gate. It excludes a second RESERVED writer but still permits new SHARED readers. Exact v0.7.2 commands such as Project add/create can read authoritative DB state and then perform clone/legacy Treehouse work before their eventual DB registration; allowing a new reader during cutover therefore leaves an external-effect race.

Mechanical proof #525 at exact head `5f985170f6ece5d60fc9f083bfef70b0d8bbc667` passed the repository's Windows, Linux x64/ARM, macOS ARM/Intel, Nix, lint, contract, and E2E matrix for the required rollback-journal handoff.

The mandatory no-gap ordering is:

```text
MigrationLock held
→ open pinned mode=ro connection with foreign_keys=ON, query_only=1, bounded busy policy
→ BEGIN deferred read transaction
→ execute source reads sufficient to establish a genuine SHARED snapshot
→ validate candidate exact legacy family/layout + rollback-journal eligibility
→ read exact active DB bytes and candidate digest while SHARED is held
→ on a second pinned mode=rw connection request BEGIN EXCLUSIVE asynchronously
→ while the known SHARED snapshot is still held, positively require a fresh mode=ro reader to fail SQLITE_BUSY
→ prove active DB bytes remain candidate-identical while SHARED + observed reader barrier coexist
→ write+flush+reopen+hash a same-volume NON-AUTHORITATIVE archive candidate
→ release only the known SHARED reader
→ require OUR already-requested BEGIN EXCLUSIVE to return successfully within bounded policy
→ PRAGMA query_only=1 on that same EXCLUSIVE connection and verify it is enabled
→ re-read/revalidate exact source state and active DB digest
→ require active DB digest == candidate digest == candidate archive digest
→ establish exact legacy lock/provider quiescence while OUR EXCLUSIVE remains held
→ only then promote/publish the candidate as immutable original archive authority
```

The fresh-reader `SQLITE_BUSY` observation is a barrier observation, not proof that the PENDING lock belonged to the cutover connection. Therefore it never authorizes publication by itself. Authority advances only after **our** `BEGIN EXCLUSIVE` returns and the exact digest/state revalidation succeeds.

Do not set `query_only=1` before requesting the SQLite write transaction: #525 proves `query_only=1 → BEGIN IMMEDIATE` fails `SQLITE_READONLY` on the production driver. The production protocol instead keeps the initial snapshot on a separate genuinely read-only connection and sets `query_only=1` on the cutover connection only after OUR EXCLUSIVE acquisition returns.

A conflicting live RESERVED/PENDING/EXCLUSIVE writer prevents this handoff from completing within the bounded policy. Cutover stops/retries; it never kills a process, guesses from PID/process name, or weakens the gate.

OUR EXCLUSIVE acquisition may cause SQLite to resolve rollback-journal state as part of acquiring the database. That resolution is accepted only if the post-acquisition active DB bytes and semantic source state remain exactly candidate-identical. Otherwise the candidate is discarded/non-authoritative and automatic cutover fails closed.

WAL/SHM is not normalized by cutover. Never checkpoint, VACUUM, run the old migration ladder, or mutate data merely to make source acceptance easier.

---

# Legacy lock and provider quiescence under EXCLUSIVE

SQLite EXCLUSIVE closes new DB readers/writers, but a v0.7.2 command that passed its authoritative DB read before the PENDING barrier may still be in the middle of a filesystem/network/provider operation. Therefore SQLite locking and legacy command locks compose; neither replaces the other.

After OUR EXCLUSIVE is held and before the original archive becomes authoritative or any freeze mutation occurs, cutover must nonblockingly prove the exact Fleet-local legacy lock graph quiescent.

At minimum this includes:

```text
MigrationLock — already held by cutover
all existing permanent hashed state/.<sha256(name)>.lock rendezvous points
explicit fixed Fleet-local sidecar locks required by exact v0.7.2
state/watch.pid.lock watcher ownership rendezvous
data/projects.md.lock project-registry projection rendezvous
```

Known hashed namespaces include command-sequence locks such as task/project/send/worktree and fixed Fleet-local sidecar/config locks where present. The implementation may proactively acquire known fixed namespaces even when their rendezvous file does not yet exist.

Permanent hashed lock pathnames are rendezvous points, not evidence that a holder exists. Cutover opens each candidate pathname and attempts the same kernel file-lock primitive nonblockingly. Busy, unreadable, unopenable, disappearing/reappearing ambiguity, or unknown lock state blocks automatic cutover.

Enumeration must be race-safe. After OUR EXCLUSIVE, a newly-started exact v0.7.2 command cannot pass its first authoritative DB read. A command that passed that read before the barrier must retain its Fleet-local command/sidecar lock across any subsequent safety-relevant external mutation; this exact-source property is part of supported-source qualification. If an audited source command can perform such a mutation after authoritative DB read without a discoverable Fleet-local lock, that source is not eligible for automatic concurrent cutover.

Do not freeze user-global locks merely because exact v0.7.2 also uses them. Herdr-start, toolchain/runtime selection, and other user-global mechanisms may be shared by another Fleet. Their safety is established through typed provider/runtime observations and #519 cross-Fleet ownership rules, not by taking a global destructive/exclusive cutover lock.

While OUR EXCLUSIVE and all required Fleet-local locks are held, re-run provider/resource quiescence. Provider unavailable or unclassifiable is `unknown`, never permission to proceed.

Only after DB digest/state, Fleet-local lock graph, and provider/resource observations all agree may the candidate archive be promoted to immutable original evidence.

---

# Exact original archive before freeze

The initial file copy produced under SHARED+reader-barrier conditions is an archive **candidate**, not forensic authority.

The candidate becomes the immutable original archive only while OUR EXCLUSIVE is held, `query_only=1`, exact source state has been revalidated, all required Fleet-local legacy locks are held/quiescent, provider/resource quiescence is positive, and all three digests are equal:

```text
initial committed candidate source digest
== post-EXCLUSIVE active DB digest
== candidate archive digest
```

Required promotion ordering:

```text
OUR EXCLUSIVE held
+ query_only=1
+ exact source validated/quiescent
+ lock/provider closure positive
+ exact digest equality positive
→ flush candidate archive file
→ atomically publish deterministic immutable original-archive path
→ flush relevant parent directory
→ reopen published archive
→ require exact digest equality again
→ record original source SHA-256 and migration identity
```

No source DB mutation may occur before this immutable original archive is durable and revalidated.

Migration identity remains a documented versioned derivation from exact Fleet identity + exact **original source DB digest**. The later frozen bridge has a different digest and never replaces the original digest in migration identity or forensic provenance.

Never overwrite an existing archive identity unless exact digests prove it is the same evidence.

A failed candidate/barrier/EXCLUSIVE/quiescence attempt may leave a bounded non-authoritative temp candidate that recovery can safely delete by exact path/identity. It must never be advertised as original evidence.

Archive preserves forensic truth, not current authority.

---

# One-way frozen bridge

After the exact original archive is durable and revalidated, the same pinned `BEGIN EXCLUSIVE` transaction may cross one explicit freeze boundary while all required Fleet-local quiescence locks remain held.

The only permitted source mutations at this boundary are the exact frozen-bridge mechanism below. First set `PRAGMA query_only=0` on the same pinned EXCLUSIVE connection and verify it is disabled. Then, atomically in that already-exclusive transaction:

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

If freeze commit cannot complete, roll back and stop/retry. A failed/rolled-back freeze must leave the active source as exact supported v0.7.2 semantics; the already durable original archive may remain as non-authoritative-for-current-state forensic evidence until a later successful freeze proves the source still matches it exactly.

After freeze commit:

- a fresh v0.7.2 `Open` must fail closed as newer-than-supported before baseline schema work;
- a stale already-open/prepared v0.7.2 writer must fail every canonical table DML through the persistent guards;
- writer exclusion no longer depends on keeping the SQLite EXCLUSIVE handle open;
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
busy/unknown Fleet-local legacy command or sidecar lock
unknown provider/runtime ownership or liveness
```

Provider unavailable is `unknown`, never permission to forget state.

Legacy runtime must reconcile/terminalize first; then cutover may retry.

---

# Migration identity / archive contents

Use deterministic same-volume paths for:

```text
active state/hand.db
non-authoritative original-archive candidate
canonical temp sibling
cutover marker
immutable archive directory
original legacy hand.db archive
optional frozen-bridge recovery/retired path
manifest
allowed semantic sidecars
```

Manifest records exact original source/archive digests, Fleet ID, supported source version/layout identity, freeze-certificate version/digest, target v19 contract identity from the current #344 authority, imported Project/workspace evidence, observed physical identities/revisions, and sidecar digests.

Sidecars that participate in semantic/archive evidence are read/digested only under their exact Fleet-local lock and final provider/resource quiescence closure. Exclude repositories, credentials, tokens, caches, binaries, private-runtime bundles, and unrelated files.

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

Cutover marker records deterministic recovery evidence/phase, expected original candidate/archive/frozen-bridge/temp/target contract identities, and paths.

Marker is never authority. Corrupt/missing/stale marker cannot override a valid canonical DB or cryptographically/schema-validated archive/frozen-bridge/temp evidence.

A crash after freeze commit must remain recoverable even if the marker update did not land: exact frozen-bridge certificate + exact matching immutable original archive provide the durable binding. If that unique binding cannot be proven, fail closed.

A marker that mentions only a non-authoritative archive candidate does not upgrade that candidate into original evidence.

---

# Publication / durability

Keep publication same-volume and crash-safe.

POSIX: fsync completed files before rename and fsync relevant parent directories after publication boundaries.

Windows: close cutover-owned handles before filesystem publication, flush completed files, use same-volume write-through rename/move semantics, and treat sharing violations from stale external handles as bounded stop/retry—not copy-over/delete fallback.

Do not pretend Windows and POSIX durability primitives are identical.

Publication sequence conceptually:

```text
MigrationLock
→ read-only SHARED committed snapshot
→ request BEGIN EXCLUSIVE on pinned rw connection
→ positively observe new-reader SQLITE_BUSY while known SHARED remains held
→ prove active DB unchanged and write/flush/hash NON-AUTHORITATIVE archive candidate
→ release known SHARED reader only
→ require OUR BEGIN EXCLUSIVE to return
→ query_only=1 on OUR EXCLUSIVE connection
→ exact source/digest revalidation against candidate
→ nonblocking Fleet-local legacy lock closure
→ provider/resource quiescence revalidation
→ promote+flush+reopen/hash exact immutable original archive
→ persist advisory prepared/original-archived marker
→ query_only=0 at explicit freeze boundary
→ write exact freeze certificate + 21 guards + user_version=22
→ COMMIT freeze
→ release Fleet-local quiescence locks only after persistent bridge protection is committed
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
→ legacy wins; resume/restart cutover only under MigrationLock and a fresh SHARED→PENDING→EXCLUSIVE gate

valid exact supported v0.7.2 active + only non-authoritative archive candidate
→ legacy wins; candidate may be reused only after a fresh complete gate proves exact digest equality, otherwise discard candidate

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

- `query_only=1` before a SQLite write transaction is rejected and never used as the gate ordering;
- `BEGIN IMMEDIATE` excludes writers but still permits readers and therefore is rejected as the final cutover gate;
- a genuine read-only SHARED snapshot can coexist with a pending EXCLUSIVE request without source mutation;
- before releasing the known SHARED reader, a fresh reader observes `SQLITE_BUSY` on every supported CI platform;
- the fresh-reader busy observation alone never authorizes archive publication;
- the archive candidate remains non-authoritative until OUR EXCLUSIVE acquisition returns;
- a conflicting existing writer prevents OUR EXCLUSIVE acquisition from completing within bounded policy;
- a new old command cannot pass an authoritative DB read after the PENDING/EXCLUSIVE barrier;
- a raced/hard-killed rollback-journal writer cannot change the candidate committed DB bytes;
- OUR EXCLUSIVE acquisition/recovery must leave active DB digest/state candidate-identical or cutover refuses;
- every required Fleet-local permanent hashed lock rendezvous is nonblockingly checked/held before archive promotion;
- busy/unknown watcher ownership lock blocks cutover;
- busy/unknown `data/projects.md.lock` blocks cutover;
- a legacy command already past its DB read and mid external mutation is caught by its Fleet-local lock;
- no user-global lock is treated as Fleet-exclusive cutover authority;
- source digest/original archive authority is established only after OUR EXCLUSIVE + lock/provider closure;
- `query_only=0` is crossed only after exact original archive durability/revalidation;
- freeze certificate + all 21 guards + sentinel commit atomically;
- a prepared stale pre-freeze writer cannot mutate any guarded table after freeze;
- a fresh v0.7.2 open refuses the frozen sentinel;
- failed freeze commit rolls back to supported source semantics;
- immutable original archive bytes remain unchanged after freeze;
- every marker/candidate/archive/freeze/temp/publication rename/flush boundary;
- crash after candidate but before OUR EXCLUSIVE;
- crash after original archive but before freeze;
- crash after freeze commit before marker/temp/publication;
- exact frozen bridge + exact archive recovery;
- frozen bridge with missing/mismatched archive refusal;
- two concurrent migration processes;
- unexpected destination exists;
- Windows sharing violations leave the frozen bridge safe and retryable;
- POSIX rename/dir-fsync crash windows;
- corrupt temp and corrupt active canonical DB;
- WAL/SHM refusal and rollback-journal recovery/digest mismatch refusal;
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
- [ ] Final old-command exclusion uses the mechanically proven read-only SHARED → observed new-reader barrier → OUR EXCLUSIVE handoff; `BEGIN IMMEDIATE` and MigrationLock are not misrepresented as sufficient old-reader/writer rendezvous.
- [ ] A PENDING/new-reader-busy observation is never treated as cutover ownership; OUR successful EXCLUSIVE acquisition plus exact digest/state revalidation is mandatory.
- [ ] Fleet-local legacy command/sidecar locks and provider/resource state are positively quiescent under OUR EXCLUSIVE before original archive authority is published.
- [ ] User-global runtime/provider locks remain outside Fleet-exclusive cutover authority and obey #519 cross-Fleet safety.
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