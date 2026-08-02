package store

import (
	"errors"
	"fmt"
)

// Wrapped by migrateSchema so a caller can tell "state/hand.db will not open"
// apart from every other reason Open can fail.
var ErrSchemaNewer = errors.New("schema version newer than this build of hand supports")

// migrations lists every schema change since the version-0 baseline the
// `schema` constant in store.go builds, applied in commit order on top of it.
// A database that predates this mechanism reads PRAGMA user_version as 0 by
// sqlite's own default, and that has to mean "the schema this commit ships",
// not "unknown, refuse to proceed" - otherwise the one fleet home that exists
// stops opening the moment this merges. Appending a column addition here,
// one entry, is the whole job for a future schema change; nothing else in
// this file needs to know about it.
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
func (db *DB) migrateSchema() error {
	current, err := db.schemaVersion()
	if err != nil {
		return err
	}
	latest := len(migrations)
	if err := schemaVersionError(current, latest); err != nil {
		return err
	}
	if current == latest {
		if _, err := db.sql.Exec(schema); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		return nil
	}

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
	if _, err := db.sql.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	for version := current; version < latest; version++ {
		if err := db.applyMigration(version); err != nil {
			return err
		}
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
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
		return fmt.Errorf("record schema version %d: %w", version+1, err)
	}
	return tx.Commit()
}
