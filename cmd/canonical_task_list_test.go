package cmd

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

func TestTaskListCommandDefaultsToUnarchivedAndSelectsHistoryExplicitly(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at,retired_at)
		VALUES('project-1',?,1,'demo','2026-09-15T00:00:00Z','');
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,lifecycle,created_at,terminal_at)
		VALUES('archived','project-1',1,'archived goal','digest-a','satisfied','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z'),
		('terminal','project-1',2,'terminal goal','digest-t','abandoned','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z')`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveCanonicalV19Task(context.Background(), home, store.CanonicalV19TaskArchiveInput{
		TaskID: "archived", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
		skip string
	}{
		{args: []string{"task", "list"}, want: "terminal,project-1,2,digest-t,abandoned,none", skip: "archived,project-1"},
		{args: []string{"task", "list", "--scope", "archived"}, want: "archived,project-1,1,digest-a,satisfied,recorded", skip: "terminal,project-1"},
		{args: []string{"task", "list", "--scope", "all"}, want: "tasks[2]{id,project_id,ordinal,goal_digest,lifecycle,archive}:"},
	} {
		root := newRootCmd(devBuild("test"))
		var out strings.Builder
		root.SetOut(&out)
		root.SetArgs(test.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), test.want) || test.skip != "" && strings.Contains(out.String(), test.skip) {
			t.Fatalf("task list %v = %q, want %q and no %q", test.args, out.String(), test.want, test.skip)
		}
	}
	after, err := os.ReadFile(store.Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("task list changed database: %v", err)
	}
}

func TestTaskListCommandMissingCanonicalStoreDoesNotCreateOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HAND_HOME", home)
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"task", "list"})
	if err := root.Execute(); err == nil {
		t.Fatal("missing canonical store accepted")
	}
	if _, err := os.Stat(store.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("Task list created database: %v", err)
	}
}
