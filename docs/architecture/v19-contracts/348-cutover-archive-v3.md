---
source_issue: 348
source_title: "docs(architecture): lock v18→v19 cutover, WorkerInput non-fabrication, and legacy archival semantics"
source_url: https://github.com/atqamz/hand/issues/348
contract_version: v19-cutover-v3
supersedes_path: docs/architecture/v19-contracts/348-cutover-archive-v2.md
supersedes_blob: 94c018d0bce8c2427d01b9af89e513cfb5f3ce96
---

# Canonical persistence cutover and legacy archival contract — revision 3 candidate

This candidate corrects only fresh automatic source eligibility in revision 2. It does not modify revision-2 bytes, frozen-bridge recovery, original-archive validation, canonical import rules, or #344 DDL. It becomes normative only after an independently reviewed manifest anchors this exact Git blob and the corresponding runtime refusal. Until then revision 2 remains the landed semantic snapshot.

## Exact-source counterexample

The shipped v0.7.2 executable at tag `09b7c2b3d48458700fa5ef121f798530892ff072` runs `project sync` by reading Project authority from SQLite, closing that connection, and only then acquiring `project:<name>` through the Fleet-local file lock. The process can wait for that lock while holding no SQLite descriptor or discoverable lock. A cutover process holding EXCLUSIVE can observe the same state whether that old waiter exists or not. It can acquire and release the project lock, commit the frozen bridge, and then the old process can acquire the lock and fast-forward its clone without another DB read or write.

A separate-process reproduction used that exact executable, observed its blocked kernel lock request after its SQLite descriptor closed, committed the frozen bridge, then resumed the old process. The clone HEAD changed after `user_version=22` had committed. `SIGSTOP` only scheduled the reproduction; it is not a proposed production guard. The source-order counterexample also prevents a general safety inference on other platforms without a separate positive proof.

## Fresh automatic cutover eligibility

No exact v0.7.2 source is eligible for concurrent automatic cutover under the current mechanism. A fresh automatic cutover request must return a typed unavailable result before acquiring MigrationLock, staging an archive candidate, publishing original evidence, or mutating the source. It must not infer process cessation from a momentarily free lock, absence of a DB descriptor, a stable digest, a PID scan, or a delay. No other legacy version becomes eligible by this correction.

Read-only classification may report an exact legacy source, but must not describe it as ready for automatic cutover. Recovery of a bridge already frozen under earlier evidence remains a separate path: it may rebuild or publish only from the exact immutable original archive and certificate required by revision 2. Recovery must never start a fresh freeze from an unfrozen legacy source.

An offline migration path needs a new versioned contract and a positive trusted external cessation/isolation witness covering every old process from before its first authoritative DB read until the frozen bridge is durable. An operator assertion alone is not that witness. This revision defines no offline witness, authorization flag, or bypass API.

## Unchanged boundaries and remaining acceptance

Revision-2 original-archive, frozen-bridge, non-fabrication, fresh-target, and recovery requirements continue to govern already-frozen evidence. Exact v0.7.2 remains the only source whose semantic facts are classified for read-only inspection; source classification is not mutation permission. The target DDL and relational contract do not change here.

This fail-closed correction does not complete #348's positive cutover acceptance. A supported fresh migration requires a separately reviewed process-cessation mechanism, native POSIX and Windows proof, full provider/resource quiescence, production startup wiring, final-head integration, and #305 release qualification. No 0.8.0 release is authorized by this candidate.
