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
		('other','project-2',1,'other goal','digest-o','active','2026-09-15T00:01:00Z',''),
		('archived-z','project-2',2,'archived goal','digest-z','satisfied','2026-09-15T00:01:00Z','2026-09-15T00:02:00Z')`, fleetID, fleetID); err != nil {
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
	if err := ArchiveCanonicalV19Task(context.Background(), home, CanonicalV19TaskArchiveInput{
		TaskID: "archived-z", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: strings.Repeat("b", 64),
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
		{scope: "unarchived", ids: []string{"active", "other", "terminal"}},
		{scope: "archived", ids: []string{"archived", "archived-z"}},
		{scope: "all", ids: []string{"active", "archived", "archived-z", "other", "terminal"}},
	} {
		t.Run(test.scope, func(t *testing.T) {
			page, err := ListCanonicalV19Tasks(context.Background(), home, test.scope, "", 100)
			if err != nil {
				t.Fatal(err)
			}
			if page.NextAfter != "" {
				t.Fatalf("unexpected next cursor %q", page.NextAfter)
			}
			ids := make([]string, 0, len(page.Items))
			for _, item := range page.Items {
				ids = append(ids, item.ID)
				if item.Archived != (item.ID == "archived" || item.ID == "archived-z") {
					t.Fatalf("archive membership for %q = %v", item.ID, item.Archived)
				}
			}
			if !slices.Equal(ids, test.ids) {
				t.Fatalf("Task IDs = %v, want %v", ids, test.ids)
			}
			after := ""
			for index, want := range test.ids {
				page, err := ListCanonicalV19Tasks(context.Background(), home, test.scope, after, 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.Items[0].ID != want {
					t.Fatalf("page after %q = %#v, want %q", after, page, want)
				}
				if index == len(test.ids)-1 {
					if page.NextAfter != "" {
						t.Fatalf("last page has next cursor %q", page.NextAfter)
					}
				} else if page.NextAfter != want {
					t.Fatalf("page cursor = %q, want %q", page.NextAfter, want)
				}
				after = want
			}
			page, err = ListCanonicalV19Tasks(context.Background(), home, test.scope, after, 1)
			if err != nil || len(page.Items) != 0 || page.NextAfter != "" {
				t.Fatalf("page beyond last Task = %#v, %v", page, err)
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
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unknown", "", 100); err == nil {
		t.Fatal("unknown Task list scope accepted")
	}
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unarchived", "", 0); err == nil {
		t.Fatal("zero limit accepted")
	}
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unarchived", "", 1001); err == nil {
		t.Fatal("excessive limit accepted")
	}
	if _, err := ListCanonicalV19Tasks(context.Background(), home, "unarchived", "", 100); err == nil {
		t.Fatal("missing canonical database accepted")
	}
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("missing database created: %v", err)
	}
}

func TestListCanonicalV19TasksBoundsLargeHistory(t *testing.T) {
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
		VALUES('project-1',?,1,'demo','2026-09-15T00:00:00Z','');
		WITH RECURSIVE ids(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM ids WHERE n<240)
		INSERT INTO task(id,project_id,ordinal,goal,goal_digest,lifecycle,created_at,terminal_at)
		SELECT printf('task-%03d',n),'project-1',n,'goal','digest','active','2026-09-15T00:01:00Z','' FROM ids`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := ListCanonicalV19Tasks(context.Background(), home, "all", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 100 || page.NextAfter != "task-100" {
		t.Fatalf("first bounded page = %d rows, next %q", len(page.Items), page.NextAfter)
	}
	page, err = ListCanonicalV19Tasks(context.Background(), home, "all", page.NextAfter, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 100 || page.Items[0].ID != "task-101" || page.NextAfter != "task-200" {
		t.Fatalf("second bounded page = %#v", page)
	}
	page, err = ListCanonicalV19Tasks(context.Background(), home, "all", page.NextAfter, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 40 || page.Items[0].ID != "task-201" || page.NextAfter != "" {
		t.Fatalf("last bounded page = %#v", page)
	}
	db, err = open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, test := range []struct {
		query string
		first string
	}{
		{query: canonicalV19TaskListAllQuery, first: "SEARCH t USING INDEX sqlite_autoindex_task_1 (id>?)"},
		{query: canonicalV19TaskListUnarchivedQuery, first: "SEARCH t USING INDEX sqlite_autoindex_task_1 (id>?)"},
		{query: canonicalV19TaskListArchivedQuery, first: "SEARCH a USING COVERING INDEX sqlite_autoindex_task_archive_1 (task_id>?)"},
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN "+test.query, "", 101)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(details) == 0 || !strings.Contains(details[0], test.first) || strings.Contains(strings.Join(details, "\n"), "USE TEMP B-TREE") {
			t.Fatalf("Task list query plan = %q, want first %q and no temp sort", details, test.first)
		}
	}
}
