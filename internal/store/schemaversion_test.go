package store

import (
	"errors"
	"strings"
	"testing"
)

// migrationsContaining narrows the registered list to the entries naming
// substr. A test that exercises one real entry has to replay only that entry:
// replaying the whole list would hit "duplicate column name" on every column
// `schema` builds that the test did not drop, and naming the entry by index
// instead would silently shift the moment another commit appends one.
func migrationsContaining(substr string) []string {
	var matched []string
	for _, m := range migrations {
		if strings.Contains(m, substr) {
			matched = append(matched, m)
		}
	}
	return matched
}

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
	own := migrationsContaining("send_undelivered_message")
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

	migrations = own

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
	own := migrationsContaining("lease_id")
	t.Cleanup(func() { migrations = restore })

	migrations = []string{}
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

	migrations = own

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

// Exercises the real delivered_at/delivered_reason entry against a database
// holding a task row written before those columns existed - the live fleet
// home's shape - so a task spawned before this commit stays readable and reads
// as not delivered rather than making the whole database unopenable.
func TestDeliveredColumnsMigrateOntoAnExistingDatabase(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	own := migrationsContaining("delivered_reason")
	t.Cleanup(func() { migrations = restore })

	migrations = []string{}
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN delivered_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN delivered_reason`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`INSERT INTO task (id, project, pr) VALUES ('t1', 'no-mistakes', 'https://github.com/kunchenguid/no-mistakes/pull/597')`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = own

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen replaying the real delivered migration: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	got, found, err := reopened.ReadTask("t1")
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got.DeliveredAt != "" || got.DeliveredReason != "" {
		t.Fatalf("migrated columns not empty-defaulted: %+v", got)
	}

	got.DeliveredAt = "2026-08-03T00:00:00Z"
	got.DeliveredReason = "PR offered upstream, maintainer decides"
	if err := reopened.WriteTask(got); err != nil {
		t.Fatal(err)
	}
	reread, _, err := reopened.ReadTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if reread.DeliveredAt != got.DeliveredAt || reread.DeliveredReason != got.DeliveredReason {
		t.Fatalf("delivered mark did not survive a write to the migrated row: %+v", reread)
	}
}

// Exercises the real pane_started_at/parked_fired_for entry against a database
// holding rows written before those columns existed. pane_started_at is
// backfilled rather than left empty: the pre-migration `parked` floor read
// status_changed_at and fell back to created_at, so freezing that same value is
// what stops the migration from either sliding a live task's floor or handing a
// task promoted before the migration its scout's whole accumulated silence.
func TestPaneStartColumnsMigrateOntoAnExistingDatabase(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	own := migrationsContaining("pane_started_at")
	t.Cleanup(func() { migrations = restore })

	migrations = []string{}
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN pane_started_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE task DROP COLUMN parked_fired_for`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`INSERT INTO task (id, created_at, status_changed_at, status_changed_for)
		VALUES ('promoted', '2026-07-01T00:00:00Z', '2026-08-01T00:00:00Z', 'working')`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`INSERT INTO task (id, created_at) VALUES ('never-observed', '2026-07-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = own

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen replaying the real pane-start migration: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	promoted, found, err := reopened.ReadTask("promoted")
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if promoted.PaneStartedAt != "2026-08-01T00:00:00Z" {
		t.Fatalf("pane_started_at = %q, want the row's status_changed_at frozen as its pane start", promoted.PaneStartedAt)
	}
	if promoted.ParkedFiredFor != "" {
		t.Fatalf("parked_fired_for = %q, want empty: no fire has been recorded for this row", promoted.ParkedFiredFor)
	}

	never, _, err := reopened.ReadTask("never-observed")
	if err != nil {
		t.Fatal(err)
	}
	if never.PaneStartedAt != "2026-07-02T00:00:00Z" {
		t.Fatalf("pane_started_at = %q, want created_at for a row with no observed transition", never.PaneStartedAt)
	}
}

// Exercises the real project.upstream entry against a database holding a
// project row written before that column existed - the live fleet home's
// shape - so a project registered long ago stays readable and gains the
// column empty rather than the whole registry failing to open.
func TestProjectUpstreamColumnMigratesOntoAnExistingDatabase(t *testing.T) {
	home := t.TempDir()

	restore := migrations
	own := migrationsContaining("project ADD COLUMN upstream")
	t.Cleanup(func() { migrations = restore })

	migrations = []string{}
	existing, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`ALTER TABLE project DROP COLUMN upstream`); err != nil {
		t.Fatal(err)
	}
	if _, err := existing.sql.Exec(`INSERT INTO project (name, url, mode, position) VALUES ('no-mistakes', 'https://github.com/atqamz/no-mistakes.git', 'no-mistakes', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	migrations = own

	reopened, err := Open(home)
	if err != nil {
		t.Fatalf("reopen replaying the real project.upstream migration: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	projects, err := reopened.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "no-mistakes" || projects[0].Upstream != "" {
		t.Fatalf("ListProjects = %+v, want the pre-migration row with an empty upstream", projects)
	}

	updated, err := reopened.SetProjectUpstream("no-mistakes", "kunchenguid/no-mistakes")
	if err != nil || !updated {
		t.Fatalf("SetProjectUpstream = %v, %v", updated, err)
	}
	projects, err = reopened.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if projects[0].Upstream != "kunchenguid/no-mistakes" {
		t.Fatalf("upstream = %q, wanted it declared on the migrated row", projects[0].Upstream)
	}
}
