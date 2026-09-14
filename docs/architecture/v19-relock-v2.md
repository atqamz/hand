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

- byte count: `108973`
- SHA-256: `5285df4ae43fb61d65977061bb79a0c1e8cf498df0028df52bca4bff1b48c966`
- schema fingerprint: `3967400d6f0fdda716d48bf8ddbb1942367239ce3e08461c0bbf46279caebfbb`
- schema-defined objects: `56 tables / 38 explicit indexes / 172 triggers` (`266` SQL-bearing objects; SQLite additionally creates `87` autoindexes)
- `PRAGMA user_version = 19`

Stored compressed DDL:

- byte count: `11584`
- SHA-256: `1cc0f415ae2a05f0aa1091ca0aa551ede16ec8504d11983ad7f7342d7a334f19`
- Git blob SHA-1: `f0896754f4c171e7c486b136fc15d93335ac7282`

Schema fingerprint algorithm: SHA-256 of UTF-8 lines `type|name|tbl_name|sql`, sorted by `(type,name)`, selected from `sqlite_schema` where `name NOT LIKE 'sqlite_%'` and `sql IS NOT NULL`.

The only relational addition is `task_archive`, keyed one-to-one to exact `task.id`. It records bounded actor, time, reason, and lowercase SHA-256 evidence. Insert guards require terminal Task/Plan/Attempt lineage and absence of unresolved operations, Holds, Backoffs, resource bindings, handling-worthy unacknowledged WorkerReports, Decisions, and Repairs. Update and delete are forbidden. A later evidence insert remains possible so archive never hides or rewrites history.

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

- byte count: `53756`
- SHA-256: `36287ad41a9c13e7994a4e6a0a31cd01e90336bf64f4b035e65b5e55f4b05584`

Stored compressed proof runner:

- byte count: `10645`
- SHA-256: `a600ad60f637b50f0288fa6fd4360807e9cfbb58aac8cee057bb1ab6dbc10683`
- Git blob SHA-1: `bc69d8eea5ede35669f363d0016c1710d1fb5370`

Relock proof result on SQLite `3.53.4`: `PASS`.

The proof retains every revision-1 case and adds:

- exact DDL SHA-256, normalized schema fingerprint, object-count, foreign-key, and integrity assertions;
- one successful terminal/reconciled Task archive flow;
- `14` Task archive predicate, constraint, replay-conflict, immutability, and post-archive evidence cases;
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
