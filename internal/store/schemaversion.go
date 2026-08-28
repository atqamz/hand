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
	`ALTER TABLE attempt ADD COLUMN execution_class TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN planned_against TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN requested_profile TEXT NOT NULL DEFAULT '';
	ALTER TABLE attempt ADD COLUMN routing_source TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE task ADD COLUMN repair_code TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN repair_reason TEXT NOT NULL DEFAULT '';
	ALTER TABLE task ADD COLUMN repair_attempt_id INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE task ADD COLUMN repair_observed_at TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE IF NOT EXISTS send_attempt (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES task(id) ON DELETE CASCADE,
		attempt_id INTEGER NOT NULL REFERENCES attempt(id) ON DELETE CASCADE,
		origin TEXT NOT NULL CHECK (origin IN ('operator', 'usage-limit-resume', 'legacy-undelivered')),
		message TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending', 'not-submitted', 'submitted', 'uncertain')),
		reason_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		finalized_at TEXT NOT NULL DEFAULT '',
		usage_limit_episode INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX IF NOT EXISTS send_attempt_one_pending ON send_attempt(attempt_id) WHERE state = 'pending';
	CREATE INDEX IF NOT EXISTS send_attempt_latest ON send_attempt(task_id, attempt_id, origin, id DESC);
	CREATE INDEX IF NOT EXISTS send_attempt_latest_any ON send_attempt(task_id, attempt_id, id DESC);
	CREATE INDEX IF NOT EXISTS send_attempt_pending_lookup ON send_attempt(task_id, attempt_id, state);
	INSERT INTO send_attempt (task_id, attempt_id, origin, message, state, reason_code, created_at, finalized_at)
	SELECT task_id, id, 'legacy-undelivered', send_undelivered_message, 'uncertain',
		'legacy-undelivered-trace',
		CASE WHEN send_undelivered_at <> '' THEN send_undelivered_at ELSE created_at END,
		send_undelivered_at
	FROM attempt
	WHERE send_undelivered_message <> '';`,
	`SELECT 1;`,
	// ensureHoldInferredColumn does the real work below: a database already carrying the
	// column, unstamped past sendHardeningMigrationVersion, must not fail a plain ALTER TABLE.
	`SELECT 1;`,
	// ensureAttemptBranchColumn does the real work below, for the same reason: the base schema
	// already carries the column, so a plain ALTER TABLE would fail wherever a test or a home
	// builds its fixture from the current schema before stamping an older version onto it.
	`SELECT 1;`,
	// The real ALTER TABLEs run from ensureAcknowledgementColumns instead, guarded by column existence
	// like ensureUsageLimitEpisodeColumns: a home already carrying the current schema.Task layout when
	// its version is forced backward must replay this step without a duplicate-column error.
	`SELECT 1;`,
	`CREATE TABLE IF NOT EXISTS fleet_identity (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		fleet_id TEXT NOT NULL UNIQUE
	);`,
	// ensureProjectIdentity does the real work below, guarded by column existence like the
	// placeholders above: the base schema already carries project.id and task.project_id, so a home
	// built from it and stamped backward replays this step without a duplicate-column error.
	`SELECT 1;`,
	// ensureAttemptBriefDigestColumn does the real work below, for the same reason: the base schema
	// already carries the column, so a plain ALTER TABLE would fail wherever a test or a home
	// builds its fixture from the current schema before stamping an older version onto it.
	`SELECT 1;`,
}

// The version whose migration splits task from attempt. A database already carrying that
// layout has every earlier migration folded into it, whatever user_version claims.
const splitVersion = 8

const launchEvidenceVersion = splitVersion + 1

const teardownEvidenceVersion = launchEvidenceVersion + 1

const routingProvenanceVersion = teardownEvidenceVersion + 1

const repairMetadataVersion = routingProvenanceVersion + 1

const sendSchemaVersion = repairMetadataVersion + 1

const sendHardeningMigrationVersion = sendSchemaVersion
const holdInferredVersion = sendHardeningMigrationVersion + 1

const attemptBranchVersion = holdInferredVersion + 1

// The last version without the acknowledgement columns atqamz/hand#267 added - the read-only ladder's
// guard against selecting them from a home migrateSchema has not yet reached.
const preAcknowledgementVersion = attemptBranchVersion + 1

const acknowledgedMetadataVersion = preAcknowledgementVersion + 1

const fleetIdentityVersion = acknowledgedMetadataVersion + 1

// The last version that identified a project by its mutable name: its task table has no
// project_id, and its project table no surrogate id (atqamz/hand#388).
const projectIdentityVersion = fleetIdentityVersion + 1

// The version whose migration adds attempt.brief_digest, recorded at attempt launch so promote can
// tell a rewritten ship brief from the scout brief attempt 1 ran against (atqamz/hand#448).
const briefDigestVersion = projectIdentityVersion + 1

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

func (db *DB) hasRoutingProvenanceColumns() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name IN ('execution_class', 'planned_against', 'requested_profile', 'routing_source')`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect routing provenance columns: %w", err)
	}
	return count == 4, nil
}

func (db *DB) hasRepairMetadataColumns() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('task') WHERE name IN ('repair_code', 'repair_reason', 'repair_attempt_id', 'repair_observed_at')`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect repair metadata columns: %w", err)
	}
	return count == 4, nil
}

func (db *DB) hasHoldInferredColumn() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('hold') WHERE name = 'inferred'`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect hold inferred column: %w", err)
	}
	return count == 1, nil
}

func (db *DB) hasAttemptBranchColumn() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name = 'branch'`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect attempt branch column: %w", err)
	}
	return count == 1, nil
}

func (db *DB) hasAcknowledgementColumns() (bool, error) {
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('task') WHERE name IN ('acknowledged_at', 'acknowledged_reason', 'acknowledged_offset', 'acknowledged_digest')`).Scan(&count); err != nil {
		return false, fmt.Errorf("detect acknowledgement columns: %w", err)
	}
	return count == 4, nil
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
					routingComplete, err := db.hasRoutingProvenanceColumns()
					if err != nil {
						return err
					}
					if routingComplete && latest >= routingProvenanceVersion {
						stamp = routingProvenanceVersion
						repairComplete, err := db.hasRepairMetadataColumns()
						if err != nil {
							return err
						}
						if repairComplete && latest >= repairMetadataVersion {
							stamp = repairMetadataVersion
							holdInferredComplete, err := db.hasHoldInferredColumn()
							if err != nil {
								return err
							}
							if holdInferredComplete && latest >= holdInferredVersion {
								stamp = holdInferredVersion
								attemptBranchComplete, err := db.hasAttemptBranchColumn()
								if err != nil {
									return err
								}
								if attemptBranchComplete && latest >= attemptBranchVersion {
									stamp = attemptBranchVersion
									acknowledgementComplete, err := db.hasAcknowledgementColumns()
									if err != nil {
										return err
									}
									if acknowledgementComplete && latest >= acknowledgedMetadataVersion {
										stamp = acknowledgedMetadataVersion
									}
								}
							}
						}
					}
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
		if _, err := tx.Exec(sendSchema); err != nil {
			return fmt.Errorf("create send schema: %w", err)
		}
		if err := ensureFleetIdentityTx(tx); err != nil {
			return err
		}
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
	if version == repairMetadataVersion {
		if err := ensureUsageLimitEpisodeColumns(tx); err != nil {
			return err
		}
	}
	if version == sendHardeningMigrationVersion {
		if err := ensureSendHardeningColumns(tx); err != nil {
			return err
		}
	}
	if version == holdInferredVersion {
		if err := ensureHoldInferredColumn(tx); err != nil {
			return err
		}
	}
	if version == attemptBranchVersion {
		if err := ensureAttemptBranchColumn(tx); err != nil {
			return err
		}
	}
	if version == preAcknowledgementVersion {
		if err := ensureAcknowledgementColumns(tx); err != nil {
			return err
		}
	}
	if version == fleetIdentityVersion-1 {
		if err := ensureFleetIdentityTx(tx); err != nil {
			return err
		}
	}
	if version == projectIdentityVersion-1 {
		if err := ensureProjectIdentity(tx); err != nil {
			return err
		}
	}
	if version == briefDigestVersion-1 {
		if err := ensureAttemptBriefDigestColumn(tx); err != nil {
			return err
		}
	}
	if err := recordSchemaVersion(tx, version+1); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureUsageLimitEpisodeColumns(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info('attempt') WHERE name IN ('usage_limit_episode', 'usage_limit_stuck_episode')`)
	if err != nil {
		return fmt.Errorf("inspect usage-limit episode columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("inspect usage-limit episode columns: %w", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect usage-limit episode columns: %w", err)
	}
	for _, column := range []string{"usage_limit_episode", "usage_limit_stuck_episode"} {
		if found[column] {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE attempt ADD COLUMN ` + column + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add %s: %w", column, err)
		}
	}
	return nil
}

// A plain ALTER TABLE in migrations would duplicate the column on a database the version-0 stamp
// logic already recognized as carrying it, since that logic stops checking at repairMetadataVersion
// and leaves everything past it to run forward regardless of what the live columns already are.
func ensureHoldInferredColumn(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('hold') WHERE name = 'inferred'`).Scan(&count); err != nil {
		return fmt.Errorf("inspect hold inferred column: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE hold ADD COLUMN inferred INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add hold inferred column: %w", err)
	}
	return nil
}

func ensureAttemptBranchColumn(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name = 'branch'`).Scan(&count); err != nil {
		return fmt.Errorf("inspect attempt branch column: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE attempt ADD COLUMN branch TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add attempt branch column: %w", err)
	}
	return nil
}

func ensureAttemptBriefDigestColumn(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name = 'brief_digest'`).Scan(&count); err != nil {
		return fmt.Errorf("inspect attempt brief_digest column: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE attempt ADD COLUMN brief_digest TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add attempt brief_digest column: %w", err)
	}
	return nil
}

func ensureAcknowledgementColumns(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info('task') WHERE name IN ('acknowledged_at', 'acknowledged_reason', 'acknowledged_offset', 'acknowledged_digest')`)
	if err != nil {
		return fmt.Errorf("inspect acknowledgement columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("inspect acknowledgement columns: %w", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect acknowledgement columns: %w", err)
	}
	columns := []struct{ name, ddl string }{
		{"acknowledged_at", "TEXT NOT NULL DEFAULT ''"},
		{"acknowledged_reason", "TEXT NOT NULL DEFAULT ''"},
		{"acknowledged_offset", "INTEGER NOT NULL DEFAULT 0"},
		{"acknowledged_digest", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		if found[column.name] {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE task ADD COLUMN ` + column.name + ` ` + column.ddl); err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}
	return nil
}

// Gives every registered project a durable surrogate id and every task a reference to it, inside
// the transaction applyMigration already wraps this step in, so a home that fails partway keeps the
// whole name-keyed layout. Rebuilt, not altered: sqlite cannot turn name into a plain unique label.
func ensureProjectIdentity(tx *sql.Tx) error {
	var taskColumn int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('task') WHERE name = 'project_id'`).Scan(&taskColumn); err != nil {
		return fmt.Errorf("inspect task project_id column: %w", err)
	}
	if taskColumn == 0 {
		if _, err := tx.Exec(`ALTER TABLE task ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add task project_id column: %w", err)
		}
	}
	var projectColumn int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('project') WHERE name = 'id'`).Scan(&projectColumn); err != nil {
		return fmt.Errorf("inspect project id column: %w", err)
	}
	if projectColumn == 0 {
		if _, err := tx.Exec(`CREATE TABLE project_identity (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			url TEXT NOT NULL,
			mode TEXT NOT NULL,
			position INTEGER NOT NULL,
			upstream TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO project_identity (id, name, url, mode, position, upstream)
		SELECT '` + projectIDPrefix + `' || lower(hex(randomblob(` + projectIDBytes + `))), name, url, mode, position, upstream FROM project;
		DROP TABLE project;
		ALTER TABLE project_identity RENAME TO project;`); err != nil {
			return fmt.Errorf("give projects a surrogate identity: %w", err)
		}
	}
	// Only the rows that have no identity yet, so replaying this step never reattaches a task
	// whose project was removed to whatever project now answers to that name.
	if _, err := tx.Exec(`UPDATE task SET project_id = COALESCE((SELECT id FROM project WHERE project.name = task.project), '')
		WHERE project_id = ''`); err != nil {
		return fmt.Errorf("backfill task project identity: %w", err)
	}
	return nil
}

func ensureSendHardeningColumns(tx *sql.Tx) error {
	if err := ensureUsageLimitEpisodeColumns(tx); err != nil {
		return err
	}
	var sendTable bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'send_attempt')`).Scan(&sendTable); err != nil {
		return fmt.Errorf("inspect send-attempt table: %w", err)
	}
	if sendTable {
		var episodeColumn bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('send_attempt') WHERE name = 'usage_limit_episode')`).Scan(&episodeColumn); err != nil {
			return fmt.Errorf("inspect send-attempt episode column: %w", err)
		}
		if !episodeColumn {
			if _, err := tx.Exec(`ALTER TABLE send_attempt ADD COLUMN usage_limit_episode INTEGER NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("add send-attempt usage-limit episode: %w", err)
			}
		}
		for _, index := range []string{
			`CREATE INDEX IF NOT EXISTS send_attempt_latest ON send_attempt(task_id, attempt_id, origin, id DESC)`,
			`CREATE INDEX IF NOT EXISTS send_attempt_latest_any ON send_attempt(task_id, attempt_id, id DESC)`,
			`CREATE INDEX IF NOT EXISTS send_attempt_pending_lookup ON send_attempt(task_id, attempt_id, state)`,
		} {
			if _, err := tx.Exec(index); err != nil {
				return fmt.Errorf("create send-attempt index: %w", err)
			}
		}
	}
	return nil
}
