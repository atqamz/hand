# Canonical v19 routing relock candidate — revision 4

This is the complete relational candidate for the missing #323 request/fallback history. It is not an accepted replacement authority, a permanent main anchor, a completed router, or release qualification. Prior snapshots, compressed artifacts, proofs and manifests remain unchanged. Independent #344 review is required before dependent routing writes.

## Exact candidate

- DDL: v19-v4.sql.gz
- Reconstructed bytes: 112243
- Reconstructed SHA-256: 4b3f4bf99a241a728c01c0de71e92fade2decee75bb3671fcef80cf329abc19f
- Compressed bytes: 12360
- Compressed SHA-256: b841d5cd68f66595ca3954a09a1f08a47752a8d57b1977916d956f40188fcd7b
- Git blob SHA-1: 716fd9c9b20ed9597ec382d70c8880dd3a7b82e2
- Schema fingerprint: 77c31959a7b47fd8d4035d4fcf9f425b9712b16eb413572b4af9c46d373beae8
- Objects: 57 tables / 39 explicit indexes / 175 triggers; 271 SQL-bearing objects, 88 SQLite autoindexes
- PRAGMA user_version = 19

Fingerprint remains SHA-256 of UTF-8 type|name|tbl_name|sql lines ordered by type,name, excluding sqlite_% and NULL SQL.

## Smallest added history

Attempt gains four nullable immutable request fields: profile_override, harness_override, model_override, effort_override. NULL means absent. Empty profile/harness is invalid; explicit empty model/effort remains distinct from absent and must still pass the owning runtime validation. Values are bounded to 512 characters. Final selected fields retain their original meaning. IS NOT comparisons prevent changing either NULL to a value or a value to NULL.

AttemptFallbackStep stores only exact Attempt identity, deterministic ordinal, rejected Profile/Harness/model/effort tuple, one of the five frozen positive rejection reasons, a lowercase SHA-256 observation digest and observed_at. Composite primary key indexes exact ordered history. Rows cannot be updated or deleted. Insertion requires exact current Project/Task/Plan/Attempt lineage, ordinal MAX+1, and no existing Attempt external operation. The existing external_operation_attempt index serves that guard; no extra index or generic provenance table is needed.

The owning future writer must atomically insert Attempt and its complete preceding rejection chain after effective-config/currentness revalidation. SQL shape cannot establish availability, authorization, the truth behind an evidence digest, an effect-free provider, or whether two inserts occurred in the same caller transaction. Exhausted/invalid/unknown/stale resolution must roll back the entire Attempt. Do not expose a general late-append fallback API.

## Mechanical proof

- Runner: v19-proof-v4.py.gz
- Reconstructed bytes: 75043
- Reconstructed SHA-256: 49a54866e46aed2ffb1338c7b5171a0e97095ac30f02cf2c64c86f352c4725c1
- Compressed bytes: 14595
- Compressed SHA-256: 810546d999408cb4a4a725a88251ae0da66efe864068afde8f990b5560779bba
- Git blob SHA-1: c3e0ab68bdde1bcf8a790384383cbb32d68c7de2

Reconstruct with gzip -dc and run the Python runner against the reconstructed SQL with --json. Current local result: PASS on SQLite 3.53.1. All 32 prior adversarial, 25 archive, 12 report-tail, representative flow, 12 operation kinds, FK/integrity and cutover non-fabrication cases are retained. Added: 32 request/fallback cases and one bounded ordered-history query, giving 13 indexed query checks. Cutover also proves zero fabricated fallback rows. Pinned Go-driver and native qualification evidence are separate.

## Compatibility and review boundary

Prior exact fingerprint 47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e is incompatible despite numeric version 19. Preserve existing development data; do not migrate or overwrite it implicitly. All earlier incompatible fingerprints remain refused. Production schema/cutover consumers must select these exact bytes atomically with the candidate input manifest. A permanent manifest must subsequently name the actual independently reviewed landed content revision; no placeholder commit or fabricated main anchor is valid.

The dependency graph and native/runtime qualification requirements are unchanged. No route definitions are copied into SQLite, no isolation provider selection returns, no routing_source/generic result/blob is introduced, and no provider, wake, acknowledgement, lifecycle, cleanup or release gate is relaxed.
