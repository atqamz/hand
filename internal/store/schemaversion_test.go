package store

import (
	"errors"
	"testing"
)

// A fresh database is built by `schema`, which already carries every
// registered migration, so it is stamped straight to the latest version
// rather than 0 - only a database that predates the mechanism entirely reads
// as 0 (TestExistingBaselineDatabaseOpensCleanly below).
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

// The mechanism has to treat a real fleet home - version 0, tables already
// present - as the known baseline, not as unversioned-and-therefore-suspect,
// or the one home that exists stops working the moment a migration lands.
func TestExistingBaselineDatabaseOpensCleanly(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTask(sampleTask()); err != nil {
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

	got, found, err := reopened.ReadTask(sampleTask().ID)
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got != sampleTask() {
		t.Fatalf("reopen lost state: got %+v", got)
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

// The real hand.db this mechanism has to keep working is exactly the shape
// this sets up: tables already created by the pre-mechanism schema,
// user_version at its sqlite default of 0, no meta row claiming a version.
// Registering a migration step is meant to be the whole job for a column
// addition to such a database: one entry, applied automatically without an
// operator running anything, and cheap to run again on the next open.
func TestPendingMigrationAppliesAutomaticallyAndOnlyOnce(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	t.Cleanup(func() { migrations = restore })

	// Empty migrations for this first open, so the fresh database it stamps
	// reads as version 0 - the pre-mechanism baseline this test needs -
	// rather than picking up whatever this build's real migrations already
	// registered.
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
	version, err := db.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("schemaVersion = %d, want 1", version)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A second open re-running `ALTER TABLE ADD COLUMN` against a column that
	// is already there fails loudly - so this only passes if the migration
	// did not run twice.
	second, err := Open(home)
	if err != nil {
		t.Fatalf("second open re-ran an already-applied migration: %v", err)
	}
	defer func() { _ = second.Close() }()
}

// Adding a column puts it in the `schema` constant, so new databases are built
// with it, and appends the matching ALTER TABLE to `migrations`, so existing
// ones gain it. A brand-new database must take only the first of those: the
// column is already there, and replaying the migration would fail with
// "duplicate column name" on every fresh home. `mode` stands in for such a
// column - the baseline `project` table already has it.
func TestFreshDatabaseSkipsAMigrationTheSchemaAlreadyBuilds(t *testing.T) {
	restore := migrations
	migrations = []string{`ALTER TABLE project ADD COLUMN mode TEXT NOT NULL DEFAULT ''`}
	t.Cleanup(func() { migrations = restore })

	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("fresh database replayed a migration its schema already builds: %v", err)
	}
	version, err := db.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("schemaVersion = %d, want 1", version)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Stamped, not merely skipped: the next open must not read it as pending.
	second, err := Open(home)
	if err != nil {
		t.Fatalf("reopening a stamped fresh database: %v", err)
	}
	defer func() { _ = second.Close() }()
}

// Exercises the real migrations entry this commit registers, rather than a
// swapped-in stand-in like the tests above, so a syntax error in the actual
// ALTER TABLE statements would fail here instead of only in a synthetic one.
func TestSendUndeliveredColumnsMigrateOntoAnExistingDatabase(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	t.Cleanup(func() { migrations = restore })

	// Empty migrations for this first open, so the fresh database it builds
	// reads as version 0; `schema` still creates the two new columns (it can't
	// be swapped the way `migrations` can), so they are dropped by hand to put
	// the database back into the pre-migration shape this test needs.
	migrations = []string{}
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN send_undelivered_message`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN send_undelivered_at`); err != nil {
		t.Fatal(err)
	}
	// WriteTask always targets the full, current taskColumns, which still
	// names the two dropped columns - so this row goes in with a raw insert
	// against the pre-migration column set instead.
	if _, err := existing.sql.Exec(`INSERT INTO task (id) VALUES ('t1')`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	// Only through this step: a later migration's column is still present from
	// `schema`, so replaying it here would fail on "duplicate column name".
	migrations = restore[:1]

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen replaying the real send_undelivered migration: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, found, err := reopened.ReadTask("t1")
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got.SendUndeliveredMessage != "" || got.SendUndeliveredAt != "" {
		t.Fatalf("migrated columns not empty-defaulted: %+v", got)
	}
}

// The live fleet home holds rows written before lease_id existed, and they have
// to keep being readable through the migration rather than only after their task
// is respawned - so the column arrives empty on every one of them, which is
// exactly what worktree.CheckCollision's path fallback keys on.
func TestLeaseIDColumnMigratesOntoAnExistingDatabase(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	t.Cleanup(func() { migrations = restore })

	// Only the migrations before the lease_id step, so the database this first
	// open builds sits exactly one step behind it - the shape a fleet home
	// upgraded to this commit is in - however many migrations land later.
	// `schema` still creates lease_id (it cannot be swapped the way `migrations`
	// can), so the column is dropped by hand to complete that shape.
	migrations = restore[:1]
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN lease_id`); err != nil {
		t.Fatal(err)
	}
	// WriteTask always targets the full, current taskColumns, which still names
	// the dropped column - so this row goes in with a raw insert against the
	// pre-migration column set instead.
	if _, err := existing.sql.Exec(`INSERT INTO task (id, worktree) VALUES ('t1', '/w/nsr')`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	// Only through the lease_id step: a later migration's column is still present
	// from `schema`, so replaying it here would fail on "duplicate column name".
	migrations = restore[:2]

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen replaying the real lease_id migration: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, found, err := reopened.ReadTask("t1")
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got.LeaseID != "" {
		t.Fatalf("migrated column not empty-defaulted: %+v", got)
	}
	if got.Worktree != "/w/nsr" {
		t.Fatalf("migration lost the pre-existing row's worktree: %+v", got)
	}
}
