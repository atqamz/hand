package cmd

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
)

func TestTaskHoldListFindsResolvedHistoryAfterArchive(t *testing.T) {
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
		VALUES('task-1','project-1',1,'finish','digest','active','2026-09-15T00:01:00Z','');
		INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
		VALUES('hold-1','task-1',1,'operator','first','digest-1','2026-09-15T00:02:00Z'),
		('hold-2','task-1',2,'blocked','second','digest-2','2026-09-15T00:03:00Z');
		INSERT INTO task_hold_resolution(hold_id,resolution,resolved_at,evidence_digest)
		VALUES('hold-1','released','2026-09-15T00:04:00Z','resolution-1'),
		('hold-2','superseded','2026-09-15T00:05:00Z','resolution-2');
		UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T00:06:00Z' WHERE id='task-1'`, fleetID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveCanonicalV19Task(context.Background(), home, store.CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T00:07:00Z", Reason: "complete", EvidenceDigest: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "registry-home"))
	registryPath, err := registry.Path()
	if err != nil {
		t.Fatal(err)
	}
	registryDB, err := registry.OpenAt(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, fleetID, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}
	otherID := createHelpFleet(t, filepath.Join(t.TempDir(), "other"))
	insertHelpRegistryClaim(t, registryPath, home, otherID)
	before, err := os.ReadFile(store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
		next string
	}{
		{args: []string{"task", "hold", "list", "task-1", "--limit", "1"}, want: "hold-1,1,operator,released", next: "next_after_ordinal: 1"},
		{args: []string{"task", "hold", "list", "task-1", "--limit", "1", "--after-ordinal", "1"}, want: "hold-2,2,blocked,superseded", next: "next_after_ordinal: 0"},
	} {
		root := newRootCmd(devBuild("test"))
		var out strings.Builder
		root.SetOut(&out)
		root.SetArgs(test.args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), test.want) || !strings.Contains(out.String(), test.next) {
			t.Fatalf("task hold list %v = %q, want %q and %q", test.args, out.String(), test.want, test.next)
		}
	}
	after, err := os.ReadFile(store.Path(home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("TaskHold list changed database: %v", err)
	}
}
