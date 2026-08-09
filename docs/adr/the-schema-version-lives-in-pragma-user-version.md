# Schema versioning uses PRAGMA user_version

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#111, atqamz/hand#48, atqamz/hand#78, atqamz/hand#128, atqamz/hand#136
- PRs: none single

## Context

`CREATE TABLE IF NOT EXISTS` adds a missing table but silently leaves an existing table without newly declared columns. Fresh test databases therefore cannot prove an older fleet home upgrades safely.

## Decision

The sqlite file's `PRAGMA user_version` gates ordered migrations. The current schema creates fresh databases directly at the latest version; existing databases apply pending steps transactionally under the schema lock. A database newer than the binary is refused before other statements run.

[`internal/store/schemaversion.go`](../../internal/store/schemaversion.go) and its tests own the exact migration protocol.

## Rejected alternatives

- A metadata row can diverge from the tables it describes.
- Treating version zero as unknown would reject every database created before versioning existed.
- Replaying migrations against fresh current-schema databases produces duplicate-column failures.
- Guessing at a newer schema risks writes the old binary cannot represent.

## Consequences

Adding a column requires a current-schema edit and an ordered migration, plus a legacy-import backfill when the empty value is not valid for old rows.
