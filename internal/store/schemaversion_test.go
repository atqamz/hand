package store

import (
	"errors"
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

	history, found, err := reopened.ReadTaskHistory(sampleTask().ID)
	if err != nil || !found {
		t.Fatalf("ReadTaskHistory = %v, %v", found, err)
	}
	assertTaskProjection(t, history.Task, sampleTask())
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
