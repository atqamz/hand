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
