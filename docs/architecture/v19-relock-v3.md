# Canonical v19 relock artifact — revision 3

This directory contains the versioned canonical v19 DDL candidate and its mechanical relock proof for atqamz/hand#344. It adds only the exact WorkerReport canonical-tail constraint and index required by atqamz/hand#347 and PR #581. Every prior DDL, proof, relock, and contract snapshot remains immutable and addressable.

This content does not claim a permanent `main` manifest anchor. The follow-up anchor change must name the immutable merged revision containing this complete content set.

## Exact DDL candidate

Stored artifact: `v19-v3.sql.gz`

Deterministic reconstruction:

```sh
gzip -dc docs/architecture/v19-v3.sql.gz > /tmp/hand-v19-v3.sql
```

Reconstructed DDL:

- byte count: `109332`
- SHA-256: `6d405f3a454ff92e91fdb5b574e52fedb3969d0021fb4c0d47e2f92a88d9114b`
- schema fingerprint: `47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e`
- schema-defined objects: `56 tables / 39 explicit indexes / 172 triggers` (`267` SQL-bearing objects; SQLite additionally creates `87` autoindexes)
- `PRAGMA user_version = 19`

Stored compressed DDL:

- byte count: `11636`
- SHA-256: `7899c0854766b3eb479e69a6c23b6061adbcb4ce02fcf38eae4dde0af6c06d28`
- Git blob SHA-1: `3308ba8650f81703585a40eb1088b8e0dd54dfa1`

Schema fingerprint algorithm: SHA-256 of UTF-8 lines `type|name|tbl_name|sql`, sorted by `(type,name)`, selected from `sqlite_schema` where `name NOT LIKE 'sqlite_%'` and `sql IS NOT NULL`.

The only relational change is named unique index `worker_report_attempt_source_order(attempt_id, source_end_offset)`. It preserves the existing `worker_report_attempt_latest` observation-time index. Source-boundary uniqueness rejects different immutable prefixes at one Attempt offset and makes the maximum boundary deterministic. Offset alone does not prove historical bytes.

The WorkerReport writer must use one `BEGIN IMMEDIATE` transaction and one bounded indexed tail query. A later append is accepted only when its attested predecessor's exact `(id, source_prefix_digest, source_end_offset)` tuple equals the relational tail and the resulting offset is strictly greater. Exact replay converges without insertion. A same-offset different prefix, restored-old predecessor, or stale Attempt/ExecutorBinding refuses. Missing or contradictory checkpoint evidence uses exact full replay or refuses; the disposable checkpoint never becomes semantic authority.

No WorkerReport column, acknowledgement relation, report state, source-provider mechanism, or currentness/acknowledgement/progression authority changes. No persisted predecessor or generic cursor/lineage infrastructure is added.

## Mechanical proof

Stored runner: `v19-proof-v3.py.gz`

Reconstruct and run:

```sh
gzip -dc docs/architecture/v19-v3.sql.gz > /tmp/hand-v19-v3.sql
gzip -dc docs/architecture/v19-proof-v3.py.gz > /tmp/hand-v19-proof-v3.py
python3 /tmp/hand-v19-proof-v3.py /tmp/hand-v19-v3.sql --json
```

Reconstructed proof runner:

- byte count: `68756`
- SHA-256: `508c77b0c6ecec11736eada2bd1d4298ac0ea8196602b0cdea78240f93945990`

Stored compressed proof runner:

- byte count: `13350`
- SHA-256: `af7e8750a266f599d199ca315850229747b7a4edbdf0b4a2fef52246fcb706ef`
- Git blob SHA-1: `2170cabc5a4ad409c71f2369a489964fde3bee4f`

Relock proof result on SQLite `3.53.4`: `PASS`.

The proof retains every revision-2 case and adds:

- `12` WorkerReport canonical-tail cases covering initial report, exact replay, append, multiple records, restored-old checkpoint fork, equal and different offsets, stale Attempt and ExecutorBinding evidence, truncation/prefix contradiction, checkpoint loss/corruption full replay, and large-history small append;
- one bounded `worker_report_attempt_source_order` `EXPLAIN QUERY PLAN` check, for `12` indexed hot-query checks total;
- the retained `32` adversarial/currentness/immutability/aliasing cases and `25` Task archive cases;
- v18→v19 target construction with zero fabricated Task archive, execution, input, report, or effect rows.

## Equal-version compatibility

The numeric version remains `19`; therefore exact fingerprint validation is mandatory. Development databases carrying prior fingerprints `067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e` or `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` are recognized historical canonical layouts but are incompatible with this candidate. Each fails closed with `ErrCanonicalV19SchemaMismatch`; Hand neither upgrades nor overwrites it. Operators must preserve/export required development evidence and create a fresh exact target through the reviewed workflow. Unknown v19 fingerprints fail identically.

## Dependency and qualification boundary

Proof and embedded-schema tests use the unchanged dependency graph: `modernc.org/sqlite v1.58.0`, `golang.org/x/sys v0.48.0`, their exact transitive graph, and Nix vendor hash `sha256-ZrUBysM9rEKDyrkpMgjX0cd+qX+VhSHZYNEIt6TuJrA=`.

Standalone SQL proof and Go-driver schema tests do not prove native lock/crash behavior or optional Linux OFD locking. Final native/release qualification remains required after this content and the permanent-anchor follow-up land.

Any semantic or byte change to the reconstructed revision-3 DDL invalidates this relock and requires a new versioned artifact, hashes, fingerprint, mechanical proof, and atqamz/hand#344 architecture review.
