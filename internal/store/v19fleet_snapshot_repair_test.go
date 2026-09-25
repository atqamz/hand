package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadCanonicalV19FleetSnapshotShowsOnlyOpenRepairsOnActiveLineage(t *testing.T) {
	ctx := context.Background()
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(ctx, fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	resolved := canonicalV19RepairWriterInput("repair-resolved", CanonicalV19RepairTarget{TaskID: "task-1"})
	if err := CreateCanonicalV19Repair(ctx, fixture.Home, resolved); err != nil {
		t.Fatal(err)
	}
	if err := ResolveCanonicalV19Repair(ctx, fixture.Home, CanonicalV19RepairResolveInput{
		RepairID: resolved.ID, Resolution: "repaired", ResolvedAt: "2026-09-24T00:04:00Z", EvidenceDigest: "digest-resolution",
	}); err != nil {
		t.Fatal(err)
	}
	taskOpen := canonicalV19RepairWriterInput("repair-task", CanonicalV19RepairTarget{TaskID: "task-1"})
	if err := CreateCanonicalV19Repair(ctx, fixture.Home, taskOpen); err != nil {
		t.Fatal(err)
	}
	planOpen := canonicalV19RepairWriterInput("repair-plan", CanonicalV19RepairTarget{PlanID: "plan-root"})
	if err := CreateCanonicalV19Repair(ctx, fixture.Home, planOpen); err != nil {
		t.Fatal(err)
	}
	attemptOpen := canonicalV19RepairWriterInput("repair-attempt", CanonicalV19RepairTarget{AttemptID: "attempt-1"})
	if err := CreateCanonicalV19Repair(ctx, fixture.Home, attemptOpen); err != nil {
		t.Fatal(err)
	}
	projectScoped := canonicalV19RepairWriterInput("repair-project", CanonicalV19RepairTarget{ProjectID: "project-1"})
	if err := CreateCanonicalV19Repair(ctx, fixture.Home, projectScoped); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(ctx, fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("Repair snapshot mutated canonical database: %v", err)
	}
	if len(snapshot.OpenRepairs) != 3 {
		t.Fatalf("open Repairs = %#v", snapshot.OpenRepairs)
	}
	byID := make(map[string]CanonicalV19SnapshotRepair, len(snapshot.OpenRepairs))
	for _, repair := range snapshot.OpenRepairs {
		byID[repair.ID] = repair
	}
	if got := byID[taskOpen.ID]; got.TargetKind != "task" || got.TargetID != "task-1" || got.TaskID != "task-1" ||
		got.RepairCode != taskOpen.RepairCode || got.EvidenceDigest != taskOpen.EvidenceDigest || got.CreatedAt != taskOpen.CreatedAt {
		t.Fatalf("task-targeted Repair = %#v", got)
	}
	if got := byID[planOpen.ID]; got.TargetKind != "plan" || got.TargetID != "plan-root" || got.TaskID != "task-1" {
		t.Fatalf("plan-targeted Repair = %#v", got)
	}
	if got := byID[attemptOpen.ID]; got.TargetKind != "attempt" || got.TargetID != "attempt-1" || got.TaskID != "task-1" {
		t.Fatalf("attempt-targeted Repair = %#v", got)
	}
	if _, ok := byID[projectScoped.ID]; ok {
		t.Fatalf("project-scoped Repair unexpectedly nested under active Task lineage: %#v", byID)
	}
	if _, ok := byID[resolved.ID]; ok {
		t.Fatalf("resolved Repair unexpectedly still open: %#v", byID)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-24T00:06:00Z' WHERE id='project-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	retired, err := ReadCanonicalV19FleetSnapshot(ctx, fixture.Home)
	if err != nil || len(retired.OpenRepairs) != 3 {
		t.Fatalf("retired Project hid active Task Repairs: %#v, %v", retired.OpenRepairs, err)
	}
}

func TestCanonicalV19SnapshotOpenRepairsQueryUsesActiveIndexes(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + canonicalV19SnapshotOpenRepairsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{
		"task_active_by_project", "plan_active_by_task", "attempt_active_by_plan",
		"repair_target_task", "repair_target_plan", "repair_target_attempt",
	} {
		if !strings.Contains(plan.String(), index) {
			t.Fatalf("open Repair query missed %s:\n%s", index, plan.String())
		}
	}
	if strings.Contains(plan.String(), "SCAN rt ") || strings.Contains(plan.String(), "SCAN r ") ||
		strings.Contains(plan.String(), "SCAN rr ") {
		t.Fatalf("open Repair query scans Repair family tables instead of exact seeks:\n%s", plan.String())
	}
}
