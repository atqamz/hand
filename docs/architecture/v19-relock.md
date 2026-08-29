# Canonical v19 relock artifact

This directory contains the exact canonical v19 DDL authority and its mechanical relock proof for issue #344.

## Exact DDL authority

Stored artifact: `v19.sql.gz`

Deterministic reconstruction:

```sh
gzip -dc docs/architecture/v19.sql.gz > /tmp/hand-v19.sql
```

The stored gzip stream was produced with `gzip -n -9` so it carries no source filename or timestamp.

Reconstructed DDL:

- byte count: `103445`
- SHA-256: `81118c2e982be7e08c0f8bf3bbb980e2ec4c5bffbbc7d419e9952732ad36c58a`
- schema fingerprint: `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8`
- schema-defined objects: `55 tables / 38 indexes / 169 triggers` (`262` SQL-bearing objects; SQLite additionally creates `86` autoindexes)
- `PRAGMA user_version = 19`

Stored compressed DDL:

- byte count: `10841`
- SHA-256: `6e92cb72ad52c135a0cb8ae8f6352f1ff2c938a289ae1e313a9ce1d6a9e42399`

Schema fingerprint algorithm: SHA-256 of UTF-8 lines `type|name|tbl_name|sql`, sorted by `(type,name)`, selected from `sqlite_schema` where `name NOT LIKE 'sqlite_%'` and `sql IS NOT NULL`.

## Mechanical proof

Stored runner: `v19-proof.py.gz`

Reconstruct and run:

```sh
gzip -dc docs/architecture/v19.sql.gz > /tmp/hand-v19.sql
gzip -dc docs/architecture/v19-proof.py.gz > /tmp/hand-v19-proof.py
python3 /tmp/hand-v19-proof.py /tmp/hand-v19.sql --json
```

Reconstructed proof runner:

- byte count: `47091`
- SHA-256: `406aa847ec8e622f97d8c2100141268b3f401bb43303c05022f3fe30a607f38e`

Stored compressed proof runner:

- byte count: `9451`
- SHA-256: `5b0c9b246a62a45669759bcf14e2717d18fb23b547a0b173670d3d240e619930`

Relock proof result on SQLite `3.46.1`: `PASS`.

The proof covers:

- fresh database `foreign_key_check` empty and `integrity_check = ok`;
- representative canonical Fleet → Project → Task → Plan → Attempt execution;
- all 12 external-operation kinds, including WorkerWake and exact resource cleanup;
- WorkerInput ordering, Answer-origin binding, WorkerInputAcknowledgement, WorkerReport and report acknowledgement;
- TaskHold, AttemptBackoff, and Repair evidence/resolution semantics;
- `32` adversarial/currentness/immutability/aliasing tests;
- `10` indexed hot-query `EXPLAIN QUERY PLAN` checks;
- v18→v19 cutover target construction with exactly zero fabricated execution/input/effect rows;
- absence of canonical semantic `Send`, `ExecutorSignal`, Treehouse/generic Isolation, Supervisor-session/conversation authority, polymorphic Repair ownership, and mutable pending/held shortcuts.

Any semantic or byte change to the reconstructed DDL invalidates this relock and requires a new artifact, hashes, fingerprint, mechanical proof, and #344 architecture review.