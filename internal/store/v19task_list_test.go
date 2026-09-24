package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestListCanonicalV19TasksUsesExactArchiveMembershipAndKeepsLateOperationVisible(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet")
	fleetID, err := InitializeCanonicalV19(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at,retired_at)
		VALUES('project-2',?,2,'second','2026-09-15T00:00:00Z',''),
		('project-1',?,1,'first','2026-09-15T00:00:00Z','');
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,lifecycle,created_at,terminal_at)
		VALUES('archived','project-1',1,'archived goal','digest-a','satisfied','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z'),
		('terminal','project-1',2,'terminal goal','digest-t','abandoned','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z'),
		('active','project-1',3,'active goal','digest-c','active','2026-09-15T00:01:00Z',''),
		('other','project-2',1,'other goal','digest-o','active','2026-09-15T00:01:00Z','')`, fleetID, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ArchiveCanonicalV19Task(context.Background(), home, CanonicalV19TaskArchiveInput{
		TaskID: "archived", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	db, err = open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO external_operation(id,kind,adapter_ref,operation_key,request_digest,
		project_id,task_id,primary_scope_kind,primary_scope_key,created_at,state_changed_at)
		VALUES('late-operation','publication','adapter','late-key','digest','project-1','archived',
		'publication','artifact-1','2026-09-15T01:03:00Z','2026-09-15T01:03:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scope string
		ids   []string
	}{
		{scope: "unarchived", ids: []string{"terminal", "active", "other"}},
		{scope: "archived", ids: []string{"archived"}},
		{scope: "all", ids: []string{"archived", "terminal", "active", "other"}},
	} {
		t.Run(test.scope, func(t *testing.T) {
			items, err := ListCanonicalV19Tasks(context.Background(), home, test.scope)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.ID)
				if item.Archived != (item.ID == "archived") {
					t.Fatalf("archive membership for %q = %v", item.ID, item.Archived)
				}
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("Task IDs = %v, want %v", ids, test.ids)
			}
		})
	}
	view, err := ReadCanonicalV19Task(context.Background(), home, "archived")
	if err != nil || view.Archive == nil || view.Lifecycle != "satisfied" {
		t.Fatalf("exact archived Task = %#v, %v", view, err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.UnresolvedOperations) != 1 || snapshot.UnresolvedOperations[0].ID != "late-operation" || snapshot.UnresolvedOperations[0].TaskID != "archived" {
		t.Fatalf("late unresolved operation = %#v", snapshot.UnresolvedOperations)
	}
	after, err := os.ReadFile(Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("Task reads changed database: %v", err)
	}
}

func TestListCanonicalV19TasksRejectsUnknownScopeAndMissingStore(t *testing.T) {
	home := t.TempDir()
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unknown"); err == nil {
		t.Fatal("unknown Task list scope accepted")
	}
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unarchived"); err == nil {
		t.Fatal("missing canonical database accepted")
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("missing database created: %v", err)
	}
}
