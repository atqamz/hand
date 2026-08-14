package store

import (
	"errors"
	"sync"
	"testing"
)

func TestFreshOpenRecordsSchemaVersionAtLatest(t *testing.T) {
	db, _ := openTemp(t)
	version, err := db.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schemaVersion = %d, want %d", version, len(migrations))
	}
}

func TestExistingBaselineDatabaseOpensCleanly(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSample(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopening an existing baseline database: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	history, found, err := reopened.ReadTaskHistory(sampleTask().ID)
	if err != nil || !found {
		t.Fatalf("ReadTaskHistory = %v, %v", found, err)
	}
	got := history.Task
	got.ActiveAttemptID = 0
	want := sampleTask()
	want.ActiveAttemptID = 0
	if got != want {
		t.Fatalf("task = %+v, want %+v", got, want)
	}
	if history.ActiveAttempt == nil || history.ActiveAttempt.Ordinal != 1 || history.ActiveAttempt.Lifecycle != AttemptRunning {
		t.Fatalf("active attempt = %+v", history.ActiveAttempt)
	}
}

func TestOpenRefusesADatabaseNewerThanThisBuild(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(home)
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("Open = %v, want ErrSchemaNewer", err)
	}
}

func TestPendingMigrationAppliesAutomaticallyAndOnlyOnce(t *testing.T) {
	home := t.TempDir()
	restore := migrations
	t.Cleanup(func() { migrations = restore })

	migrations = []string{}
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := existing.AddProject(Project{Name: "nsr", URL: "u", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = []string{`ALTER TABLE project ADD COLUMN note TEXT NOT NULL DEFAULT ''`}
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	var note string
	if err := db.sql.QueryRow(`SELECT note FROM project WHERE name = 'nsr'`).Scan(&note); err != nil {
		t.Fatalf("migrated column not usable: %v", err)
	}
	if version, err := db.schemaVersion(); err != nil || version != 1 {
		t.Fatalf("schemaVersion = %d, %v", version, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(home)
	if err != nil {
		t.Fatalf("second open re-ran an already-applied migration: %v", err)
	}
	defer func() { _ = second.Close() }()
}

func TestFreshDatabaseSkipsAMigrationTheSchemaAlreadyBuilds(t *testing.T) {
	restore := migrations
	migrations = []string{`ALTER TABLE project ADD COLUMN mode TEXT NOT NULL DEFAULT ''`}
	t.Cleanup(func() { migrations = restore })

	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("fresh database replayed a migration its schema already builds: %v", err)
	}
	if version, err := db.schemaVersion(); err != nil || version != 1 {
		t.Fatalf("schemaVersion = %d, %v", version, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(home)
	if err != nil {
		t.Fatalf("reopening a stamped fresh database: %v", err)
	}
	defer func() { _ = second.Close() }()
}

func TestFailedMigrationDoesNotAdvanceUserVersion(t *testing.T) {
	home := t.TempDir()
	restore := migrations
	t.Cleanup(func() { migrations = restore })

	migrations = nil
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = []string{`
		ALTER TABLE project ADD COLUMN migration_marker TEXT NOT NULL DEFAULT '';
		SELECT missing_migration_table.value FROM missing_migration_table;
	`}
	if _, err := Open(home); err == nil {
		t.Fatal("Open succeeded for an intentionally failing migration")
	}

	migrations = nil
	check, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	version, err := check.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("user_version = %d, want 0 after rollback", version)
	}
	var columns int
	if err := check.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('project') WHERE name = 'migration_marker'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatal("failed migration left its schema change behind")
	}
}

func TestConcurrentOpenersMigrateAReal040DatabaseOnce(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: sqlDB, home: home}
	values := []any{
		"legacy", "nsr", "ship", "data/legacy/brief.md", "", false, "", 0, "", false, "", "",
		"claude", "opus", "high", "/w/nsr", "lease-7", "default", "wA", "wA:tB", "wA:pC",
		"2026-07-24T10:00:00Z", "2026-07-24T10:30:00Z", "2026-07-24T11:00:00Z", "working", true,
		"working", "still working", "", "", "", "", 0,
	}
	if _, err := db.sql.Exec(legacy040Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO task (
		id, project, kind, brief, pr, merge_executed, merge_executed_at, report_offset, report_digest,
		merge_announced, delivered_at, delivered_reason, harness, model, effort, worktree, lease_id,
		herdr_session, herdr_workspace_id, herdr_tab_id, herdr_pane_id, created_at, pane_started_at,
		status_changed_at, status_changed_for, done_verified, last_report_state, last_report_note,
		send_undelivered_message, send_undelivered_at, parked_fired_for, usage_limit_retry_at,
		usage_limit_attempts) VALUES (`+placeholders(len(values))+`)
	`, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	const openers = 2
	errs := make([]error, openers)
	var wg sync.WaitGroup
	for i := range openers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			opened, err := Open(home)
			if err == nil {
				err = opened.Close()
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", i, err)
		}
	}

	check, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	version, err := check.schemaVersion()
	if err != nil || version != len(migrations) {
		t.Fatalf("schema version = %d, %v, want %d", version, err, len(migrations))
	}
	history, found, err := check.ReadTaskHistory("legacy")
	if err != nil || !found || history.ActiveAttempt == nil {
		t.Fatalf("migrated history = %+v, %v, %v", history, found, err)
	}
	if history.ActiveAttempt.Worktree != "/w/nsr" || history.ActiveAttempt.Herdr.PaneID != "wA:pC" {
		t.Fatalf("migrated attempt lost execution evidence: %+v", history.ActiveAttempt)
	}
}

func TestUnstampedSplitLayoutUsesTransactionalStampBeforeNextMigration(t *testing.T) {
	home := t.TempDir()
	restore := migrations
	t.Cleanup(func() { migrations = restore })

	migrations = restore[:8]
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: sqlDB, home: home}
	if _, err := db.sql.Exec(legacy040Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	split, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := split.sql.Exec(`PRAGMA user_version = 0`); err != nil {
		_ = split.Close()
		t.Fatal(err)
	}
	if err := split.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = restore
	check, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = check.Close() }()
	var evidenceColumns int
	if err := check.sql.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('attempt') WHERE name IN ('launch_submitted_at', 'launch_confirmed_at')`).Scan(&evidenceColumns); err != nil {
		t.Fatal(err)
	}
	if evidenceColumns != 2 {
		t.Fatalf("launch evidence columns = %d, want 2", evidenceColumns)
	}
	version, err := check.schemaVersion()
	if err != nil || version != len(migrations) {
		t.Fatalf("schema version = %d, %v, want %d", version, err, len(migrations))
	}
	if version == 0 {
		t.Fatal("unstamped split layout remained at version 0")
	}
}
