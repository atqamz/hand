package cmd

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestTaskArchiveCommandAppendsExactCanonicalFact(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet")
	fleetID, err := store.InitializeCanonicalV19(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	db, err := sql.Open("sqlite", "file:"+store.Path(home)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at,retired_at)
		VALUES('project-1',?,1,'demo','2026-09-15T00:00:00Z','');
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,lifecycle,created_at,terminal_at)
		VALUES('task-1','project-1',1,'finish','goal-digest','abandoned','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd(devBuild("test"))
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{
		"task", "archive", "task-1",
		"--actor-kind", "operator", "--actor-ref", "operator-1",
		"--archived-at", "2026-09-15T01:02:03Z",
		"--reason", "completed and reconciled",
		"--evidence-digest", strings.Repeat("a", 64),
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task_id: task-1") {
		t.Fatalf("archive output = %q", out.String())
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1' AND actor_ref='operator-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("archive facts = %d, want one", count)
	}
}

func TestTaskShowRetainsArchivedTaskAndRefusesMissingID(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet")
	fleetID, err := store.InitializeCanonicalV19(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	db, err := sql.Open("sqlite", "file:"+store.Path(home)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at,retired_at)
		VALUES('project-1',?,1,'demo','2026-09-15T00:00:00Z','');
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,lifecycle,created_at,terminal_at)
		VALUES('task-1','project-1',1,'finish','goal-digest','abandoned','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	show := func(id string) (string, error) {
		root := newRootCmd(devBuild("test"))
		var out strings.Builder
		root.SetOut(&out)
		root.SetArgs([]string{"task", "show", id})
		err := root.Execute()
		return out.String(), err
	}
	before, err := show("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "lifecycle: abandoned") || !strings.Contains(before, "archive: none") {
		t.Fatalf("unarchived Task = %q", before)
	}
	if err := store.ArchiveCanonicalV19Task(context.Background(), home, store.CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	after, err := show("task-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"task_id: task-1", "lifecycle: abandoned", "archive: recorded", "actor_ref: operator-1", "reason: completed and reconciled"} {
		if !strings.Contains(after, want) {
			t.Fatalf("archived Task = %q, missing %q", after, want)
		}
	}
	if _, err := show("missing"); err == nil {
		t.Fatal("missing Task was accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("read changed archive facts: %d", count)
	}
}
