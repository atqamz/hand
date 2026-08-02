package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Wrapped by migrateSchema so a caller can tell "state/hand.db will not open"
// apart from every other reason Open can fail.
var ErrSchemaNewer = errors.New("schema version newer than this build of hand supports")

// migrations lists every schema change since the version-0 baseline the
// `schema` constant in store.go builds, applied in commit order to a database
// that already exists. A database that predates this mechanism reads PRAGMA
// user_version as 0 by sqlite's own default, and that has to mean "the schema
// this commit ships", not "unknown, refuse to proceed" - otherwise the one
// fleet home that exists stops opening the moment this merges. A future
// column addition is two edits that stay in step: the column goes into
// `schema`, so every new database is built with it, and the matching ALTER
// TABLE is appended here, so every database that already exists gains it on
// its next open. A brand-new database is built by `schema` alone and never
// replays this list; see migrateSchema.
var migrations = []string{}

func (db *DB) schemaVersion() (int, error) {
	var version int
	if err := db.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func schemaVersionError(current, latest int) error {
	if current <= latest {
		return nil
	}
	return fmt.Errorf("state/hand.db is schema version %d, newer than this build of hand supports (version %d) - upgrade hand before opening it: %w",
		current, latest, ErrSchemaNewer)
}

// migrateSchema is the first statement Open runs against the database: a
// version newer than this binary knows is refused before the baseline
// `schema` even executes, so an old hand never guesses at a layout it does
// not understand. Bringing an old database up to date takes a lock because
// sqlite's per-statement locking cannot make "add this column, then bump
// user_version" atomic across a whole open - without it, two hand processes
// racing to migrate the same freshly-upgraded home would have the loser see
// "duplicate column name" instead of a clean, idempotent no-op.
//
// A database with no tables yet is built by `schema` and stamped straight to
// the latest version, migrations skipped: `schema` is the current layout,
// every column a registered migration adds included, so replaying that list
// on top of it would fail with "duplicate column name" on every fresh home
// while the already-migrated ones kept working - the test-passes,
// production-fails asymmetry this whole mechanism exists to remove.
func (db *DB) migrateSchema() error {
	current, err := db.schemaVersion()
	if err != nil {
		return err
	}
	latest := len(migrations)
	if err := schemaVersionError(current, latest); err != nil {
		return err
	}
	if current < latest {
		unlock, err := Lock(db.home, SchemaLock, false)
		if err != nil {
			return fmt.Errorf("lock schema migration: %w", err)
		}
		defer unlock()

		// Re-read: another process may have finished the migration while this one
		// waited for the lock.
		current, err = db.schemaVersion()
		if err != nil {
			return err
		}
		if err := schemaVersionError(current, latest); err != nil {
			return err
		}
	}

	// Before the exec below creates the tables it asks about.
	isNew, err := db.isNewDatabase()
	if err != nil {
		return err
	}
	if err := db.createSchema(isNew, latest); err != nil {
		return err
	}
	if isNew {
		return nil
	}
	for version := current; version < latest; version++ {
		if err := db.applyMigration(version); err != nil {
			return err
		}
	}
	return nil
}

// One transaction, because a new database's tables and the version stamp that
// says migrations are already folded into them have to land together: a crash
// between the two would leave the migrated columns present at version 0, and
// every later open replaying those migrations against them - a freshly
// initialized home no operator step short of a hand-written PRAGMA reopens.
func (db *DB) createSchema(isNew bool, latest int) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return fmt.Errorf("begin create schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if isNew {
		if err := recordSchemaVersion(tx, latest); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// A database without the `task` table has never had `schema` run against it,
// so no column a migration would add can already be present.
func (db *DB) isNewDatabase() (bool, error) {
	var name string
	err := db.sql.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'task'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("detect a new database: %w", err)
	}
	return false, nil
}

// Always from inside the transaction that made the schema match the version
// being recorded, so the two cannot disagree on disk.
func recordSchemaVersion(tx *sql.Tx, version int) error {
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return fmt.Errorf("record schema version %d: %w", version, err)
	}
	return nil
}

// One transaction per step, so a migration that fails partway leaves
// user_version at the last step that fully committed rather than at a state
// that does not match what actually landed.
func (db *DB) applyMigration(version int) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return fmt.Errorf("begin schema migration to version %d: %w", version+1, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(migrations[version]); err != nil {
		return fmt.Errorf("migrate schema to version %d: %w", version+1, err)
	}
	if err := recordSchemaVersion(tx, version+1); err != nil {
		return err
	}
	return tx.Commit()
}
