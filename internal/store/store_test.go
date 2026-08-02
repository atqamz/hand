package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, home
}

func sampleTask() Task {
	return Task{
		ID: "fix-login", Project: "nsr", Kind: KindShip, Harness: "claude",
		Model: "opus", Effort: "high", Worktree: "/w/nsr", Brief: "data/fix-login/brief.md",
		Herdr:           Herdr{Session: "default", WorkspaceID: "wA", TabID: "wA:tB", PaneID: "wA:pC"},
		PR:              "https://github.com/o/nsr/pull/1",
		MergeExecuted:   true,
		MergeExecutedAt: "2026-07-24T12:00:00Z",
		ReportOffset:    42,
		MergeAnnounced:  true,
		DoneVerified:    true,
		CreatedAt:       "2026-07-24T10:00:00Z",

		StatusChangedAt: "2026-07-24T11:00:00Z", StatusChangedFor: "working",
		LastReportState: "working", LastReportNote: "on it",
	}
}

func TestWriteReadPreservesEveryField(t *testing.T) {
	db, _ := openTemp(t)
	want := sampleTask()
	if err := db.WriteTask(want); err != nil {
		t.Fatal(err)
	}

	got, found, err := db.ReadTask(want.ID)
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got != want {
		t.Fatalf("round trip lost a field:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestWriteTaskOverwritesInPlace(t *testing.T) {
	db, _ := openTemp(t)
	task := sampleTask()
	if err := db.WriteTask(task); err != nil {
		t.Fatal(err)
	}
	task.PR = "https://github.com/o/nsr/pull/2"
	if err := db.WriteTask(task); err != nil {
		t.Fatal(err)
	}

	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].PR != task.PR {
		t.Fatalf("ListTasks = %+v", tasks)
	}
}

func TestReadTaskReportsAMissingTaskWithoutAnError(t *testing.T) {
	db, _ := openTemp(t)
	_, found, err := db.ReadTask("nope")
	if err != nil || found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
}

func TestDeleteTask(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.WriteTask(sampleTask()); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTask("fix-login"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteTask("fix-login"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("second delete = %v, want ErrTaskNotFound", err)
	}
	exists, err := db.TaskExists("fix-login")
	if err != nil || exists {
		t.Fatalf("TaskExists = %v, %v", exists, err)
	}
}

func TestProjectsKeepRegistrationOrder(t *testing.T) {
	db, _ := openTemp(t)
	for _, name := range []string{"nsr", "universe", "yes2infra"} {
		if err := db.AddProject(Project{Name: name, URL: "git@github.com:o/" + name + ".git", Mode: "direct-pr"}); err != nil {
			t.Fatal(err)
		}
	}

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "nsr,universe,yes2infra" {
		t.Fatalf("order = %v", names)
	}
}

func TestAddProjectRejectsADuplicateName(t *testing.T) {
	db, _ := openTemp(t)
	p := Project{Name: "nsr", URL: "git@github.com:o/nsr.git", Mode: "direct-pr"}
	if err := db.AddProject(p); err != nil {
		t.Fatal(err)
	}
	if err := db.AddProject(p); !errors.Is(err, ErrProjectExists) {
		t.Fatalf("got %v, want ErrProjectExists", err)
	}
}

func TestRemoveProject(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.AddProject(Project{Name: "nsr", URL: "u", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}
	removed, err := db.RemoveProject("nsr")
	if err != nil || !removed {
		t.Fatalf("RemoveProject = %v, %v", removed, err)
	}
	removed, err = db.RemoveProject("nsr")
	if err != nil || removed {
		t.Fatalf("second RemoveProject = %v, %v", removed, err)
	}
}

func writeLegacyTask(t *testing.T, home string, task Task) {
	t.Helper()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home), task.ID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenImportsLegacyTaskFiles(t *testing.T) {
	home := t.TempDir()
	want := sampleTask()
	writeLegacyTask(t, home, want)

	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, found, err := db.ReadTask(want.ID)
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got != want {
		t.Fatalf("import lost a field:\ngot  %+v\nwant %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(Dir(home), want.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("legacy file still in state/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(LegacyDir(home), want.ID+".json")); err != nil {
		t.Fatalf("legacy file not preserved under state/migrated: %v", err)
	}
}

// Running the migration twice is what actually happens: every hand command opens
// the store. The second open must find nothing to do and change nothing.
func TestMigrationIsIdempotent(t *testing.T) {
	home := t.TempDir()
	writeLegacyTask(t, home, sampleTask())

	first, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	updated := sampleTask()
	updated.PR = "https://github.com/o/nsr/pull/99"
	if err := first.WriteTask(updated); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	got, found, err := second.ReadTask(updated.ID)
	if err != nil || !found {
		t.Fatalf("ReadTask = %v, %v", found, err)
	}
	if got.PR != updated.PR {
		t.Fatalf("a re-run overwrote live state with the archived file: PR = %q", got.PR)
	}
	tasks, err := second.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("ListTasks = %d tasks, want 1", len(tasks))
	}
}

// A legacy file the migration cannot read is a loud failure, not a skipped task:
// silently continuing would present a partial fleet as the whole one.
func TestOpenRefusesAnUnreadableLegacyFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(home), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := Open(home)
	if err == nil {
		_ = db.Close()
		t.Fatal("Open accepted an unparseable legacy task file")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("error does not name the file: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("refusing consumed the file it could not read: %v", statErr)
	}
}

func TestMigratedMarker(t *testing.T) {
	db, _ := openTemp(t)
	done, err := db.Migrated("projects.md")
	if err != nil || done {
		t.Fatalf("Migrated before marking = %v, %v", done, err)
	}
	if err := db.MarkMigrated("projects.md"); err != nil {
		t.Fatal(err)
	}
	done, err = db.Migrated("projects.md")
	if err != nil || !done {
		t.Fatalf("Migrated after marking = %v, %v", done, err)
	}
}

func TestPathsLiveUnderTheStateDirectory(t *testing.T) {
	home := "/fleet"
	if got := Path(home); got != filepath.Join(home, "state", "hand.db") {
		t.Errorf("Path = %q", got)
	}
	if got := IndexPath(home); got != filepath.Join(home, "state", "index.db") {
		t.Errorf("IndexPath = %q", got)
	}
	if got := LegacyDir(home); got != filepath.Join(home, "state", "migrated") {
		t.Errorf("LegacyDir = %q", got)
	}
}

// A fleet home can sit anywhere an operator put it, and the pragmas require a
// file: URI, where `%`, `#` and `?` are syntax rather than filename.
func TestOpenHandlesAHomePathWithURISyntaxInIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet 100%#a?b")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := Open(home)
	if err != nil {
		t.Fatalf("open %q: %v", home, err)
	}
	defer func() { _ = db.Close() }()

	if err := db.WriteTask(sampleTask()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ReadTask(sampleTask().ID)
	if err != nil || !ok {
		t.Fatalf("ReadTask = %v, %v", ok, err)
	}
	if got.Project != sampleTask().Project {
		t.Fatalf("got %+v, want the task written back", got)
	}
	if _, err := os.Stat(Path(home)); err != nil {
		t.Fatalf("stat %s: %v, want the database inside the home", Path(home), err)
	}
}
