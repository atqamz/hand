package store

import (
	"errors"
	"testing"
)

func TestFreshOpenRecordsSchemaVersionZero(t *testing.T) {
	db, _ := openTemp(t)
	version, err := db.schemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("schemaVersion = %d, want 0", version)
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

// Registering a migration step is meant to be the whole job for a column
// addition to a database that already exists: one entry, applied
// automatically, without an operator running anything, and cheap to run again
// on the next open.
func TestPendingMigrationAppliesAutomaticallyAndOnlyOnce(t *testing.T) {
	home := t.TempDir()
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

	restore := migrations
	migrations = []string{`ALTER TABLE project ADD COLUMN note TEXT NOT NULL DEFAULT ''`}
	t.Cleanup(func() { migrations = restore })

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

// The real hand.db this mechanism has to keep working is exactly this shape:
// tables already created by the pre-mechanism schema, user_version at its
// sqlite default of 0, no meta row claiming a version. A pending migration
// still has to reach it without any operator step.
func TestMigrationReachesADatabaseCreatedBeforeThisMechanismExisted(t *testing.T) {
	home := t.TempDir()
	pre, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.AddProject(Project{Name: "nsr", URL: "u", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}
	if err := pre.Close(); err != nil {
		t.Fatal(err)
	}

	restore := migrations
	migrations = []string{`ALTER TABLE project ADD COLUMN note TEXT NOT NULL DEFAULT ''`}
	t.Cleanup(func() { migrations = restore })

	post, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = post.Close() }()

	var note string
	if err := post.sql.QueryRow(`SELECT note FROM project WHERE name = 'nsr'`).Scan(&note); err != nil {
		t.Fatalf("pre-existing database did not gain the pending column: %v", err)
	}
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
