package store

import (
	"strconv"
	"strings"
	"testing"
)

// The layout a fleet home carried at fleetIdentityVersion: a project keyed by its mutable name
// and a task holding a literal copy of it, which is what the surrogate identity replaces.
const preProjectIdentityProjectTable = `DROP TABLE project;
CREATE TABLE project (name TEXT PRIMARY KEY, url TEXT NOT NULL, mode TEXT NOT NULL, position INTEGER NOT NULL, upstream TEXT NOT NULL DEFAULT '');`

func stampPreProjectIdentity(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.sql.Exec(preProjectIdentityProjectTable); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`ALTER TABLE task DROP COLUMN project_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`PRAGMA user_version = ` + strconv.Itoa(fleetIdentityVersion)); err != nil {
		t.Fatal(err)
	}
}

func projectIdentityOf(t *testing.T, db *DB, name string) string {
	t.Helper()
	var id string
	if err := db.sql.QueryRow(`SELECT id FROM project WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("read identity for project %q: %v", name, err)
	}
	return id
}

func taskProjectIdentity(t *testing.T, db *DB, id string) string {
	t.Helper()
	var projectID string
	if err := db.sql.QueryRow(`SELECT project_id FROM task WHERE id = ?`, id).Scan(&projectID); err != nil {
		t.Fatalf("read project identity for task %q: %v", id, err)
	}
	return projectID
}

func TestMigrationGivesEveryRegisteredProjectAnIdentityAndBackfillsTasks(t *testing.T) {
	db, home := openTemp(t)
	stampPreProjectIdentity(t, db)
	if _, err := db.sql.Exec(`INSERT INTO project (name, url, mode, position, upstream) VALUES
		('nsr', 'git@github.com:o/nsr.git', 'direct-pr', 0, ''),
		('universe', 'git@github.com:o/universe.git', 'no-mistakes', 1, 'o/universe')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO task (id, project, kind, lifecycle) VALUES
		('live', 'nsr', 'ship', 'open'),
		('other', 'universe', 'scout', 'terminal'),
		('orphan', 'retired', 'ship', 'terminal')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(home)
	if err != nil {
		t.Fatalf("migrate a pre-identity home: %v", err)
	}
	defer func() { _ = migrated.Close() }()
	if version, err := migrated.schemaVersion(); err != nil || version != len(migrations) {
		t.Fatalf("schemaVersion = %d, %v, want %d", version, err, len(migrations))
	}

	nsr := projectIdentityOf(t, migrated, "nsr")
	universe := projectIdentityOf(t, migrated, "universe")
	if !strings.HasPrefix(nsr, projectIDPrefix) || len(nsr) != len(projectIDPrefix)+32 {
		t.Fatalf("nsr identity = %q, want a %s-prefixed opaque id", nsr, projectIDPrefix)
	}
	if nsr == universe {
		t.Fatalf("both projects share identity %q", nsr)
	}
	if got := taskProjectIdentity(t, migrated, "live"); got != nsr {
		t.Fatalf("task live identity = %q, want %q", got, nsr)
	}
	if got := taskProjectIdentity(t, migrated, "other"); got != universe {
		t.Fatalf("task other identity = %q, want %q", got, universe)
	}
	// "retired" names no registered project, so its lineage is not knowable from the name alone
	// and the migration refuses to guess one for it (atqamz/hand#388).
	if got := taskProjectIdentity(t, migrated, "orphan"); got != "" {
		t.Fatalf("orphan task identity = %q, want no identity", got)
	}

	projects, err := migrated.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].Name != "nsr" || projects[0].ID != nsr || projects[1].Upstream != "o/universe" {
		t.Fatalf("projects = %+v, want the pre-migration rows with identities", projects)
	}
}

func TestMigrationToProjectIdentityIsIdempotent(t *testing.T) {
	db, home := openTemp(t)
	stampPreProjectIdentity(t, db)
	if _, err := db.sql.Exec(`INSERT INTO project (name, url, mode, position, upstream) VALUES ('nsr', 'u', 'direct-pr', 0, '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO task (id, project, kind, lifecycle) VALUES ('live', 'nsr', 'ship', 'open')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	first, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	identity := projectIdentityOf(t, first, "nsr")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(home)
	if err != nil {
		t.Fatalf("reopen an already-migrated home: %v", err)
	}
	defer func() { _ = second.Close() }()
	if got := projectIdentityOf(t, second, "nsr"); got != identity {
		t.Fatalf("identity = %q after reopening, want the minted %q", got, identity)
	}
	if got := taskProjectIdentity(t, second, "live"); got != identity {
		t.Fatalf("task identity = %q, want %q", got, identity)
	}
	var projects int
	if err := second.sql.QueryRow(`SELECT COUNT(*) FROM project`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("project rows = %d, want the one row the first migration produced", projects)
	}
}

// The property atqamz/hand#388 and atqamz/hand#396 were both about: a rename is one row's
// relabelling, so there is no history to search by name and none to put back.
func TestRenameRelabelsOneRowAndLeavesTaskHistoryAlone(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.AddProject(Project{Name: "demo", URL: "u", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}
	identity := projectIdentityOf(t, db, "demo")
	if err := db.CreateTask(Task{ID: "task-1", Project: "demo", Kind: KindShip}); err != nil {
		t.Fatal(err)
	}

	renamed, err := db.RenameProject("demo", "renamed")
	if err != nil || !renamed {
		t.Fatalf("RenameProject = %v, %v", renamed, err)
	}

	if got := projectIdentityOf(t, db, "renamed"); got != identity {
		t.Fatalf("identity after rename = %q, want the unchanged %q", got, identity)
	}
	if got := taskProjectIdentity(t, db, "task-1"); got != identity {
		t.Fatalf("task identity after rename = %q, want the unchanged %q", got, identity)
	}
	var stored string
	if err := db.sql.QueryRow(`SELECT project FROM task WHERE id = 'task-1'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "demo" {
		t.Fatalf("stored task project = %q, want the rename to have left the row untouched", stored)
	}
	task, found, err := db.ReadTask("task-1")
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if task.Project != "renamed" {
		t.Fatalf("resolved task project = %q, want the live label %q", task.Project, "renamed")
	}
}

// The conflation atqamz/hand#388 reported: a name freed by a removal and claimed by a later
// project must not carry the removed project's task history into it.
func TestRecreatedProjectNameDoesNotInheritTheRemovedProjectsTasks(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.AddProject(Project{Name: "demo", URL: "u", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}
	retired := projectIdentityOf(t, db, "demo")
	if err := db.CreateTask(Task{ID: "old-task", Project: "demo", Kind: KindShip, Lifecycle: TaskTerminal}); err != nil {
		t.Fatal(err)
	}
	if removed, err := db.RemoveProject("demo"); err != nil || !removed {
		t.Fatalf("RemoveProject = %v, %v", removed, err)
	}
	if err := db.AddProject(Project{Name: "demo", URL: "u2", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	successor := projectIdentityOf(t, db, "demo")
	if successor == retired {
		t.Fatalf("recreated project reused identity %q", successor)
	}
	if err := db.CreateTask(Task{ID: "new-task", Project: "demo", Kind: KindShip}); err != nil {
		t.Fatal(err)
	}

	if got := taskProjectIdentity(t, db, "old-task"); got != retired {
		t.Fatalf("old task identity = %q, want the retired %q", got, retired)
	}
	if got := taskProjectIdentity(t, db, "new-task"); got != successor {
		t.Fatalf("new task identity = %q, want the successor %q", got, successor)
	}

	// Renaming the successor moves only its own tasks' live label; the retired project's task
	// keeps the name it was written with, because nothing is rewritten by name any more.
	if renamed, err := db.RenameProject("demo", "demo-two"); err != nil || !renamed {
		t.Fatalf("RenameProject = %v, %v", renamed, err)
	}
	old, _, err := db.ReadTask("old-task")
	if err != nil {
		t.Fatal(err)
	}
	if old.Project != "demo" || old.ProjectID != retired {
		t.Fatalf("old task = %+v, want it left under the retired project", old)
	}
	fresh, _, err := db.ReadTask("new-task")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Project != "demo-two" || fresh.ProjectID != successor {
		t.Fatalf("new task = %+v, want it following the successor's new label", fresh)
	}
}

// The read-only ladder has to keep answering for a home migrateSchema has not reached yet: at
// fleetIdentityVersion the task table has no project_id to select or to join a live label through.
func TestReadOnlyLadderReadsAPreIdentityHome(t *testing.T) {
	db, home := openTemp(t)
	stampPreProjectIdentity(t, db)
	if _, err := db.sql.Exec(`INSERT INTO project (name, url, mode, position, upstream) VALUES ('nsr', 'u', 'direct-pr', 0, '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO task (id, project, kind, lifecycle) VALUES ('live', 'nsr', 'ship', 'open')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	history, found, err := ReadTaskHistoryReadOnly(home, "live")
	if err != nil || !found {
		t.Fatalf("ReadTaskHistoryReadOnly = %v, %v", found, err)
	}
	if history.Task.Project != "nsr" || history.Task.ProjectID != "" {
		t.Fatalf("task = %+v, want the stored name and no identity", history.Task)
	}
	histories, err := ListReconciliationHistoriesReadOnly(home)
	if err != nil {
		t.Fatalf("ListReconciliationHistoriesReadOnly: %v", err)
	}
	if len(histories) != 1 || histories[0].Task.Project != "nsr" {
		t.Fatalf("histories = %+v, want the one open task", histories)
	}
}
