# The schema version lives in `PRAGMA user_version`, and a fresh database never replays migrations

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#111, atqamz/secondhand#48, atqamz/secondhand#78, atqamz/secondhand#128, atqamz/secondhand#136
- PRs: none single

## Context

Before this, `Open` applied the `schema` constant with `CREATE TABLE IF NOT EXISTS` on every open and had no version concept.
That is correct for a table an existing database is missing outright, which is why adding the `hold` table needed nothing else.
It is a silent no-op for a *column* an existing table is missing: sqlite satisfies "if not exists" at the table level and never looks at the column list again, so no error is raised and no column is added.

The asymmetry is the dangerous part.
Tests build fresh databases, so a column addition passes every test.
The one real fleet home on disk silently never gains it.

There is also only one real fleet home, and it predates any version mechanism, so whatever the mechanism is it has to open that home without refusing it.

## Decision

`Open` gates every other statement on `PRAGMA user_version`, sqlite's own counter for exactly this: no extra table, free to read, and part of the database file rather than a row a stray write could get out of sync with the tables it describes.

Version 0 is the schema the `schema` constant builds.
0 means "the baseline schema this commit ships", not "unknown, refuse to proceed".

`migrations` is an ordered list of SQL statements, one per change since that baseline, each moving `user_version` from its index to index+1.
An ordinary column addition is two edits that stay in step: the column goes into `schema` so every new database is built with it, and the matching `ALTER TABLE` is appended to `migrations` so every existing database gains it on next open.

A brand-new database never replays migrations.
`migrateSchema` checks for the `task` table first, and on that path creates the tables and stamps `user_version` straight to `len(migrations)`, both in one transaction.

A database newer than the binary is refused wrapping `ErrSchemaNewer` before a single statement runs against the tables.

Applying pending migrations takes `SchemaLock`, and each pending step on an existing database runs in its own transaction after the baseline exec.

A column whose empty default would be wrong for an existing row takes a third edit, a backfill `UPDATE` alongside its `ALTER TABLE`, and `readLegacyTask` computes the same value.

## Rejected alternatives

**A `meta` table row holding the version.**
It is a row a stray write can desynchronize from the tables it describes, and it costs a read of a table that may not exist yet.
`user_version` travels with the file.

**Treat version 0 as unknown and refuse it.**
It would stop the one fleet home that exists from opening the moment the mechanism merged.

**Freeze `schema` at the baseline forever and read every column addition out of `migrations`.**
The constant would then lie about the current layout, with nothing but prose to stop the next reader from adding a column to it.

**Keep `schema` current and replay migrations on every database, fresh ones included.**
Every fresh `hand init` breaks with "duplicate column name" while already-migrated homes keep working.
That is the tests-pass, production-fails asymmetry inverted, which is the exact failure this exists to remove.

**Stamp `user_version` in a second transaction after creating the tables.**
A crash between them leaves a home carrying the migrated columns while reading as version 0, and every later open replays those migrations against columns that are already there.

**Rely on sqlite's per-statement locking instead of `SchemaLock`.**
It cannot make "add this column, then bump the version" atomic across a whole `Open`, so two processes opening the same freshly-upgraded home both run the `ALTER TABLE`.

**Backfill every new column for safety.**
An empty retry stamp is exactly "this task is not waiting on quota", which is the honest reading of every row written before `hand` could detect a limit at all.
A backfill there would invent state.

## Consequences

Adding a column is a two-edit change with a fixed shape, and adding one to `schema` alone is now the mistake the mechanism exists to catch rather than a silent no-op.

A backfilled column is three edits, and the third is `readLegacyTask`, because a legacy JSON import lands as an `INSERT` no migration step ever runs over.

An old binary refuses a new database rather than writing malformed rows into it, so a rollback is a refusal to start rather than corruption.

The workaround that made `hold` a new table is no longer needed for its schema reason, though the teardown reason still stands on its own.
See `holds-are-their-own-table.md`.
