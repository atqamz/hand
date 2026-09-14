# Canonical v19 relock artifact — revision 2

This directory contains the versioned canonical v19 DDL candidate and its mechanical relock proof for atqamz/hand#344. It adds only the exact Task archive fact required by atqamz/hand#301. The previous artifact remains immutable and addressable at `docs/architecture/v19.sql.gz`, `docs/architecture/v19-proof.py.gz`, and `docs/architecture/v19-relock.md`.

This content does not claim a permanent `main` manifest anchor. The follow-up anchor change must name the immutable merged revision containing this complete content set.

## Exact DDL candidate

Stored artifact: `v19-v2.sql.gz`

Deterministic reconstruction:

```sh
gzip -dc docs/architecture/v19-v2.sql.gz > /tmp/hand-v19-v2.sql
```

Reconstructed DDL:

- byte count: `109107`
- SHA-256: `d4a3d0adf3a84cc56002a1b558387d2169cb771b484addcb0fd8af743e19f16c`
- schema fingerprint: `b3ba6d26db1f5aa520f248249b2e3679361283301484f7ea7c0f69ddd514e7f0`
- schema-defined objects: `56 tables / 38 explicit indexes / 172 triggers` (`266` SQL-bearing objects; SQLite additionally creates `87` autoindexes)
- `PRAGMA user_version = 19`

Stored compressed DDL:

- byte count: `11604`
- SHA-256: `f4527132e7db93d0b527514b7418606be13d3d47565359dd5a6a754a56e2f7e2`
- Git blob SHA-1: `10361016d6c6372e873d70b73ae13a9764297531`

Schema fingerprint algorithm: SHA-256 of UTF-8 lines `type|name|tbl_name|sql`, sorted by `(type,name)`, selected from `sqlite_schema` where `name NOT LIKE 'sqlite_%'` and `sql IS NOT NULL`.

The only relational addition is `task_archive`, keyed one-to-one to exact `task.id`. It records byte-bounded actor, time, reason, and an exact lowercase SHA-256 evidence digest; byte bounds prevent embedded NUL from truncating SQLite text-length validation. Insert guards require terminal Task/Plan/Attempt lineage and absence of unresolved operations, Holds, Backoffs, resource bindings, handling-worthy unacknowledged WorkerReports, Decisions, and Repairs. Update and delete are forbidden. A later evidence insert remains possible so archive never hides or rewrites history.

The archive writer must use one `BEGIN IMMEDIATE` transaction, establish required positive external resource observations before the transaction, revalidate their exact identities inside the transaction, and insert the fact only after the relational predicate passes. Unknown external ownership or liveness refuses archival. The schema evidence digest does not replace those writer obligations.

## Mechanical proof

Stored runner: `v19-proof-v2.py.gz`

Reconstruct and run:

```sh
gzip -dc docs/architecture/v19-v2.sql.gz > /tmp/hand-v19-v2.sql
gzip -dc docs/architecture/v19-proof-v2.py.gz > /tmp/hand-v19-proof-v2.py
python3 /tmp/hand-v19-proof-v2.py /tmp/hand-v19-v2.sql --json
```

Reconstructed proof runner:

- byte count: `54900`
- SHA-256: `7045446f4cbb71bc8a4faa92faf58db9329d059b38ce1aea1065f9e488dd202c`

Stored compressed proof runner:

- byte count: `10831`
- SHA-256: `88b52de5581b4d41f4f554c709cfb365da447fc7da266960b0df63c55183e876`
- Git blob SHA-1: `4a067c7df1d100a13e718091380cb310fb69c9de`

Relock proof result on SQLite `3.53.4`: `PASS`.

The proof retains every revision-1 case and adds:

- exact DDL SHA-256, normalized schema fingerprint, object-count, foreign-key, and integrity assertions;
- one successful terminal/reconciled Task archive flow;
- `18` Task archive predicate, byte-bound/NUL-tail, constraint, replay-conflict, immutability, and post-archive evidence cases;
- `32` retained adversarial/currentness/immutability/aliasing cases;
- `11` indexed hot-query `EXPLAIN QUERY PLAN` checks, including exact archive lookup;
- all `12` external-operation kinds and the six-state operation ledger;
- v18→v19 target construction with zero fabricated Task archive, execution, input, report, or effect rows.

## Equal-version compatibility

The numeric version remains `19`; therefore exact fingerprint validation is mandatory. A development database carrying the prior v19 fingerprint `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` is a recognized historical canonical layout but is incompatible with this candidate. It fails closed with `ErrCanonicalV19SchemaMismatch`; Hand neither upgrades nor overwrites it. Operators must preserve/export any required development evidence and create a fresh exact target through the reviewed workflow. Unknown v19 fingerprints fail identically.

## Dependency and qualification boundary

Proof and embedded-schema tests use the dependency graph selected by PR #582: `modernc.org/sqlite v1.58.0`, `golang.org/x/sys v0.48.0`, its exact transitive graph, and Nix vendor hash `sha256-ZrUBysM9rEKDyrkpMgjX0cd+qX+VhSHZYNEIt6TuJrA=`. This relock does not change dependencies.

Standalone SQL proof and Go-driver schema tests do not prove native lock/crash behavior or optional Linux OFD locking. Final native/release qualification remains required after this content and the permanent-anchor follow-up land.

Any semantic or byte change to the reconstructed revision-2 DDL invalidates this relock and requires a new versioned artifact, hashes, fingerprint, mechanical proof, and atqamz/hand#344 architecture review.
