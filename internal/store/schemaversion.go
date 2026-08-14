package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Wrapped by migrateSchema so a caller can tell "state/hand.db will not open"
// apart from every other reason Open can fail.
var ErrSchemaNewer = errors.New("schema version newer than this build of hand supports")

// Every schema change since the version-0 baseline `schema` builds, applied in commit order
// to a database that already exists. Version 0 is sqlite's own default, so it has to mean
// "the schema this commit ships" or the one fleet home that exists stops opening.
var migrations = []string{
	`ALTER TABLE task ADD COLUMN send_undelivered_message TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN send_undelivered_at TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE task ADD COLUMN lease_id TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE project ADD COLUMN upstream TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE task ADD COLUMN delivered_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN delivered_reason TEXT NOT NULL DEFAULT '';`,
	// The backfill freezes each row's pane start at what the pre-migration floor already
	// computed for it. created_at would hand a task promoted earlier its scout's creation
	// instant - the false `parked` this floor prevents; overstating only delays a true one.
	`ALTER TABLE task ADD COLUMN pane_started_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN parked_fired_for TEXT NOT NULL DEFAULT '';
	UPDATE task SET pane_started_at = CASE WHEN status_changed_at <> '' THEN status_changed_at ELSE created_at END;`,
	// No backfill: the digest of an existing row's consumed prefix cannot be recovered from a report
	// file that may already have been rewritten, and an empty one is what the reader falls back to the
	// newline boundary for. The first tick that consumes a line records it.
	`ALTER TABLE task ADD COLUMN report_digest TEXT NOT NULL DEFAULT '';`,
	// No backfill: an empty retry stamp is exactly "this task is not limited", which
	// is the honest reading of every row written before hand could detect a limit.
	`ALTER TABLE task ADD COLUMN usage_limit_retry_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN usage_limit_attempts INTEGER NOT NULL DEFAULT 0;`,
	`DROP TABLE IF EXISTS attempt;
	CREATE TABLE task_v8 (
		id TEXT PRIMARY KEY,
		project TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		brief TEXT NOT NULL DEFAULT '',
		lifecycle TEXT NOT NULL DEFAULT 'open' CHECK (lifecycle IN ('open', 'terminal')),
		active_attempt_id INTEGER REFERENCES attempt_v8(id),
		pr TEXT NOT NULL DEFAULT '',
		merge_executed INTEGER NOT NULL DEFAULT 0,
		merge_executed_at TEXT NOT NULL DEFAULT '',
		merge_announced INTEGER NOT NULL DEFAULT 0,
		delivered_at TEXT NOT NULL DEFAULT '',
		delivered_reason TEXT NOT NULL DEFAULT '',
		report_offset INTEGER NOT NULL DEFAULT 0,
		report_digest TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE attempt_v8 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES task_v8(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL,
		lifecycle TEXT NOT NULL CHECK (lifecycle IN ('provisioning', 'running', 'completed', 'failed', 'interrupted')),
		harness TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		effort TEXT NOT NULL DEFAULT '',
		worktree TEXT NOT NULL DEFAULT '',
		lease_id TEXT NOT NULL DEFAULT '',
		herdr_session TEXT NOT NULL DEFAULT '',
		herdr_workspace_id TEXT NOT NULL DEFAULT '',
		herdr_tab_id TEXT NOT NULL DEFAULT '',
		herdr_pane_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		pane_started_at TEXT NOT NULL DEFAULT '',
		status_changed_at TEXT NOT NULL DEFAULT '',
		status_changed_for TEXT NOT NULL DEFAULT '',
		done_verified INTEGER NOT NULL DEFAULT 0,
		last_report_state TEXT NOT NULL DEFAULT '',
		last_report_note TEXT NOT NULL DEFAULT '',
		send_undelivered_message TEXT NOT NULL DEFAULT '',
		send_undelivered_at TEXT NOT NULL DEFAULT '',
		parked_fired_for TEXT NOT NULL DEFAULT '',
		usage_limit_retry_at TEXT NOT NULL DEFAULT '',
		usage_limit_attempts INTEGER NOT NULL DEFAULT 0,
		UNIQUE (task_id, ordinal)
	);
	INSERT INTO task_v8 (id, project, kind, brief, lifecycle, pr, merge_executed, merge_executed_at,
		merge_announced, delivered_at, delivered_reason, report_offset, report_digest, created_at)
	SELECT id, project, kind, brief, 'open', pr, merge_executed, merge_executed_at,
		merge_announced, delivered_at, delivered_reason, report_offset, report_digest, created_at
	FROM task;
	INSERT INTO attempt_v8 (task_id, ordinal, lifecycle, harness, model, effort, worktree, lease_id,
		herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id, created_at, pane_started_at,
		status_changed_at, status_changed_for, done_verified, last_report_state, last_report_note,
		send_undelivered_message, send_undelivered_at, parked_fired_for, usage_limit_retry_at, usage_limit_attempts)
	SELECT id, 1, 'running', harness, model, effort, worktree, lease_id,
		herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id, created_at, pane_started_at,
		status_changed_at, status_changed_for, done_verified, last_report_state, last_report_note,
		send_undelivered_message, send_undelivered_at, parked_fired_for, usage_limit_retry_at, usage_limit_attempts
	FROM task;
	UPDATE task_v8 SET active_attempt_id = (SELECT id FROM attempt_v8 WHERE task_id = task_v8.id);
	DROP TABLE task;
	ALTER TABLE task_v8 RENAME TO task;
	ALTER TABLE attempt_v8 RENAME TO attempt;
	CREATE UNIQUE INDEX attempt_one_active ON attempt(task_id) WHERE lifecycle IN ('provisioning', 'running');`,
	`ALTER TABLE attempt ADD COLUMN launch_submitted_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN launch_confirmed_at TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE attempt ADD COLUMN teardown_terminal_attempt TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN teardown_disposition TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN teardown_herdr_state TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN teardown_worktree_state TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN teardown_completion_state TEXT NOT NULL DEFAULT '';`,
}

// The version whose migration splits task from attempt. A database already carrying that
// layout has every earlier migration folded into it, whatever user_version claims.
const splitVersion = 8

const launchEvidenceVersion = splitVersion + 1

const teardownEvidenceVersion = launchEvidenceVersion + 1

// Reports whether the task table already carries the split layout. The attempt table cannot
// answer this: createSchema builds it on every home before any migration runs, while an
// existing task table keeps whatever columns it was created with.
func (db *DB) taskIsSplit() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('task') WHERE name = 'active_attempt_id'`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect the split task layout: %w", err)
	}
	return count != 0, nil
}

func (db *DB) hasLaunchEvidenceColumns() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name IN ('launch_submitted_at', 'launch_confirmed_at')`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect launch evidence columns: %w", err)
	}
	return count == 2, nil
}

func (db *DB) hasTeardownEvidenceColumns() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name IN ('teardown_terminal_attempt', 'teardown_disposition', 'teardown_herdr_state', 'teardown_worktree_state', 'teardown_completion_state')`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect teardown evidence columns: %w", err)
	}
	return count == 5, nil
}

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

// The first statement Open runs against the database: a version newer than this binary
// knows is refused before the baseline `schema` even executes, so an old hand never
// guesses at a layout it does not understand.
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
		// sqlite's per-statement locking cannot make "add this column, then bump
		// user_version" atomic across a whole open. Without the lock, two processes racing
		// to migrate the same home leave the loser a "duplicate column name" error.
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
	// `schema` is the current layout, every column a registered migration adds included, so
	// replaying the list over a fresh home fails with "duplicate column name" while the
	// already-migrated homes keep working - a test-passes, production-fails asymmetry.
	if isNew {
		return nil
	}
	// Version 0 means both "the pre-versioning baseline" and "already split, never stamped",
	// and only the task table's own layout tells them apart. Stamped rather than returned, so
	// a migration added after the split still applies to a home that arrives here unstamped.
	if current == 0 && latest >= splitVersion {
		split, err := db.taskIsSplit()
		if err != nil {
			return err
		}
		if split {
			stamp := splitVersion
			complete, err := db.hasLaunchEvidenceColumns()
			if err != nil {
				return err
			}
			if complete && latest >= launchEvidenceVersion {
				stamp = launchEvidenceVersion
				teardownComplete, err := db.hasTeardownEvidenceColumns()
				if err != nil {
					return err
				}
				if teardownComplete && latest >= teardownEvidenceVersion {
					stamp = teardownEvidenceVersion
				}
			}
			if err := db.stampSchemaVersion(stamp); err != nil {
				return err
			}
			current = stamp
		}
	}
	for version := current; version < latest; version++ {
		if err := db.applyMigration(version); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) stampSchemaVersion(version int) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return fmt.Errorf("begin schema version stamp %d: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := recordSchemaVersion(tx, version); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record schema version %d: %w", version, err)
	}
	return nil
}

// One transaction, because a new database's tables and the version stamp saying migrations
// are already folded into them have to land together: a crash between the two strands the
// home at version 0 with migrated columns, replaying them forever short of a raw PRAGMA.
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
