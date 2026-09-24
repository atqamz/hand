# Canonical v19 immutability relock candidate — revision 6

This is review input for #344. Revision 4 remains the embedded runtime authority. Revision 5 remains separate, unchanged review input. Neither this candidate nor an equal `user_version` upgrades an existing database.

## Exact candidate

- Source: unchanged `v19-v5.sql.gz`, reconstructed SHA-256 `e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66`
- DDL: `v19-v6.sql.gz`, Git blob `b2cf787b749b6247c3bcbed8e75b82d6e439e3cd`
- Reconstructed DDL: 150312 bytes, SHA-256 `ab04b86f04fffa18c1e058661813714bc3ffd70429b34746e533c2cbc20b31e6`
- Stored gzip: 15065 bytes, SHA-256 `3e7c6e2acac267511b2a1cc746865796d5a3b84b2cc66a245cfe314f2eb8b777`
- Schema fingerprint: `ccb9debabf5e194a5d290b6593d1616043bc433561074f09941711de685851ef`
- Objects: 57 tables, 39 explicit indexes, 289 triggers, 385 SQL-bearing objects, 33 SQLite autoindexes
- `PRAGMA user_version = 19`

The fingerprint is SHA-256 over UTF-8 `type|name|tbl_name|sql` lines ordered by `(type,name)`, excluding `sqlite_%` and NULL SQL. `v19-v6-generate.py` derives the candidate from revision 5, writes the guard overlay and deterministic gzip artifacts. It does not edit any prior artifact.

## Relational correction

Revision 5's 57 `BEFORE DELETE` triggers do not prevent SQLite `INSERT OR REPLACE` from deleting an immutable row when `recursive_triggers=OFF`. A replacement can conflict on the primary key, another unique key belonging to a different row, or a hidden `rowid` that no logical key names. The three Decision, Answer and Closure examples are symptoms of the same schema-wide bypass.

Each of the 57 tables gains `BEFORE INSERT` and `BEFORE UPDATE` guards for every unique key, including five partial unique indexes. The update guard excludes the current row by its old primary key, allowing legitimate in-place lifecycle changes while blocking `UPDATE OR REPLACE` from deleting a different row. The generator rejects unsupported index expressions, collations and predicates. All 94 unique-key collision queries use indexed seeks under SQLite 3.53.4. The 55 tables with non-integer primary keys become `STRICT, WITHOUT ROWID`, so a caller cannot address a hidden row identity outside these guards. `fleet` and `external_operation_event` retain their `INTEGER PRIMARY KEY` row identity, which their guards cover. `external_operation_event.id` also gains `CHECK (id>0)`: a fresh auto-generated ID remains valid, while an explicit negative one cannot collide with SQLite's `NEW.id=-1` preallocation sentinel. This rejects previously permitted explicit non-positive event IDs and NULL text primary keys, so consumer and cutover review must account for the narrower valid-input set.

No new table, index, operation kind, authority source or mutable second record is introduced. Existing active/terminal transitions, archive behavior, Decision lookup and WorkerReport source ordering remain unchanged.

## Proof

- Runner: `v19-proof-v6.py.gz`, Git blob `b66b4ee1aa59dccc7315e4ed4079715cc0e0fce5`
- Reconstructed runner: 8407 bytes, SHA-256 `52770c3a6f9dce971bfd391ef37ad22704705eb1dea3107880ad115ff82599ce`
- Stored gzip: 3081 bytes, SHA-256 `89ec90b7573a4a5d130556efb0e6574f9d6cd31e7a193e3f9848b32fe2244105`
- Source runner: `v19-v6-proof.py`, Git blob `3c7093191d65c3f0ab73117755d4144cbba2148e`
- Additional replacement cases: `v19-v6-no-replace-proof.py`, Git blob `86eeb735b11daf88c9e4600ed8ee392b8e4eb765`
- Generator: `v19-v6-generate.py`, Git blob `1e839bf17a9623d0b3cf86d26ef4c0bc39f6efa1`
- Generated guard overlay: `v19-v6-no-replace.sql`, Git blob `772cf685d96adc614d90c4766c9c7573dec37227`

Run `python3 docs/architecture/v19-v6-proof.py` from the repository. The compressed runner can be decompressed elsewhere and given `docs/architecture` as its sole argument. The proof pins revision-5 DDL and proof hashes, proves deterministic gzip reconstruction and exact generated overlay, checks all 57 no-delete tables and both remaining rowid tables, and reruns the previous representative, adversarial, Task archive, WorkerReport, routing, cutover and 13 adapted indexed read queries. The additional cases exercise both recursive-trigger settings, `INSERT OR REPLACE` and `UPDATE OR REPLACE`, same-key replacement, alternate unique-key replacement, explicit hidden-rowid replacement, valid Attempt retry and positive auto-generated event IDs. The pinned Go driver checks the Decision, Answer, Closure, Fleet, alternate-key, update-conflict and hidden-rowid cases. SQLite 3.53.4 and `go test -tags=test ./internal/store -count=1` passed locally.

## Authority and compatibility boundary

Independent review must validate the `WITHOUT ROWID` and event-ID semantic narrowing, all replacement paths, query plans, native driver behavior and cutover from the exact v0.7.2 source. A new manifest input may record these artifact identities, but its content commit cannot be named before that content is committed and reviewed. A permanent manifest must name the actual main landing separately. Runtime DDL, schema fingerprint and cutover authority fields must switch together only after the reviewed content identity exists. Prior v19 fingerprints remain incompatible; preserve them rather than silently migrating or overwriting them. This relock alone does not complete #323/#324 routing/configuration or #305 release qualification.
