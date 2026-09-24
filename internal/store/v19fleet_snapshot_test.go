package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCanonicalV19FleetSnapshotCoreLineage(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")
	second := canonicalV19AttemptWriterInput("attempt-2", "plan-root")
	second.CreatedAt = "2026-09-04T09:02:00Z"
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, second); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at)
		VALUES('project-2','fleet-1',2,'second','2026-09-04T06:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Task(context.Background(), fixture.Home, CanonicalV19TaskCreateInput{
		ID: "task-2", ProjectID: "project-2", Goal: "second goal", GoalDigest: "second-digest", CreatedAt: "2026-09-04T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	db, err = open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-04T08:01:00Z' WHERE id='project-2'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("snapshot changed database: %v", err)
	}
	if snapshot.Schema != "hand.fleet.core.v1" || snapshot.Completeness != "partial" || snapshot.FleetID != "fleet-1" {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.DBReadAt); err != nil {
		t.Fatalf("database read instant = %q: %v", snapshot.DBReadAt, err)
	}
	if len(snapshot.Projects) != 2 || snapshot.Projects[0].ID != "project-1" || snapshot.Projects[1].ID != "project-2" {
		t.Fatalf("project order = %#v", snapshot.Projects)
	}
	primary := snapshot.Projects[0]
	if primary.Workspace == nil || primary.Workspace.ID != "workspace-1" || primary.Policy == nil || primary.Policy.ID != "policy-1" {
		t.Fatalf("project current sources = %#v", primary)
	}
	if len(primary.Tasks) != 1 || primary.Tasks[0].ID != "task-1" || primary.Tasks[0].Plan == nil || primary.Tasks[0].Plan.ID != "plan-root" {
		t.Fatalf("primary lineage = %#v", primary.Tasks)
	}
	if attempt := primary.Tasks[0].Plan.Attempt; attempt == nil || attempt.ID != "attempt-2" || attempt.Ordinal != 2 || attempt.WorkerHarnessRef != second.WorkerHarnessRef {
		t.Fatalf("active successor = %#v", attempt)
	}
	secondary := snapshot.Projects[1]
	if secondary.RetiredAt == "" || secondary.Workspace != nil || secondary.Policy != nil || len(secondary.Tasks) != 1 || secondary.Tasks[0].Plan != nil {
		t.Fatalf("project without binding/Plan = %#v", secondary)
	}
}

func TestReadCanonicalV19FleetSnapshotRefusesMissingAndIncompatibleStores(t *testing.T) {
	missing := t.TempDir()
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), missing); err == nil {
		t.Fatal("missing canonical database accepted")
	}
	if _, err := os.Stat(Path(missing)); !os.IsNotExist(err) {
		t.Fatalf("missing database created: %v", err)
	}
	legacy := t.TempDir()
	db, err := open(Path(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), legacy); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("legacy database = %v", err)
	}
	canonical := canonicalV19TaskWriterFixture(t, false)
	db, err = open(Path(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE stray(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), canonical); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("drifted schema = %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(missing, "state")); !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatalf("missing fleet state created: %v, %v", entries, err)
	}
}

func TestReadCanonicalV19FleetSnapshotSeparatesInputFromWakeAndHistoricalEffects(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	for _, input := range []CanonicalV19WorkerInputCreateInput{
		canonicalV19WorkerInputCreateInput(launch, "input-acked", "first instruction", "digest-first"),
		canonicalV19WorkerInputCreateInput(launch, "input-open", "second instruction", "digest-second"),
	} {
		if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home,
		canonicalV19WorkerInputAcknowledgementCreateInput("input-acked", launch.BindingID)); err != nil {
		t.Fatal(err)
	}
	wake, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home,
		canonicalV19WorkerWakePrepareInput(launch, "wake-open", 2))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("input snapshot mutated database: %v", err)
	}
	if len(snapshot.UnacknowledgedInputs) != 1 || snapshot.UnacknowledgedInputs[0].ID != "input-open" ||
		snapshot.UnacknowledgedInputs[0].Ordinal != 2 || snapshot.UnacknowledgedInputs[0].ExecutorBindingID != launch.BindingID {
		t.Fatalf("current unacknowledged input = %#v", snapshot.UnacknowledgedInputs)
	}
	if len(snapshot.UnresolvedOperations) != 1 || snapshot.UnresolvedOperations[0].ID != wake.OperationID ||
		snapshot.UnresolvedOperations[0].Kind != "worker-wake" || snapshot.UnresolvedOperations[0].State != "prepared" ||
		snapshot.UnresolvedOperations[0].SessionBindingID == "" || snapshot.UnresolvedOperations[0].ExecutorBindingID != launch.BindingID {
		t.Fatalf("independent wake operation = %#v", snapshot.UnresolvedOperations)
	}
	if _, err := SubmitCanonicalV19WorkerWake(context.Background(), fixture.Home, wake.OperationID,
		"2026-09-09T05:19:00Z", "wake-submitted"); err != nil {
		t.Fatal(err)
	}
	before, err = os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("submitted wake snapshot mutated database: %v", err)
	}
	if len(snapshot.UnacknowledgedInputs) != 1 || snapshot.UnacknowledgedInputs[0].ID != "input-open" ||
		len(snapshot.UnresolvedOperations) != 1 || snapshot.UnresolvedOperations[0].State != "submitted" ||
		snapshot.UnresolvedOperations[0].PendingThroughOrdinal != 2 ||
		snapshot.UnresolvedOperations[0].AttemptID != launch.AttemptID ||
		snapshot.UnresolvedOperations[0].ExecutorBindingID != launch.BindingID {
		t.Fatalf("exact submitted wake/input source = %#v", snapshot)
	}
	if err := ClassifyCanonicalV19WorkerWake(context.Background(), fixture.Home, CanonicalV19WorkerWakeTransitionInput{
		OperationID: wake.OperationID, State: "uncertain", ObservedAt: "2026-09-09T05:19:30Z", EvidenceDigest: "wake-outcome-unknown",
	}); err != nil {
		t.Fatal(err)
	}
	before, err = os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("uncertain wake snapshot mutated database: %v", err)
	}
	if len(snapshot.UnacknowledgedInputs) != 1 || len(snapshot.UnresolvedOperations) != 1 ||
		snapshot.UnresolvedOperations[0].ID != wake.OperationID || snapshot.UnresolvedOperations[0].State != "uncertain" ||
		snapshot.UnresolvedOperations[0].SessionBindingID == "" || snapshot.UnresolvedOperations[0].ExecutorBindingID != launch.BindingID ||
		snapshot.UnresolvedOperations[0].StateEvidenceDigest != "wake-outcome-unknown" {
		t.Fatalf("uncertain wake lost exact mechanism evidence or invented acknowledgement: %#v", snapshot)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO executor_binding_termination(
		executor_binding_id,terminal_kind,observed_at,evidence_digest
	) VALUES(?, 'provider-gone', '2026-09-09T05:20:00Z', 'executor-gone')`, launch.BindingID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.UnacknowledgedInputs) != 0 || len(snapshot.UnresolvedOperations) != 1 ||
		snapshot.UnresolvedOperations[0].ID != wake.OperationID || snapshot.UnresolvedOperations[0].State != "uncertain" {
		t.Fatalf("historical input/wake separation = %#v", snapshot)
	}
}
