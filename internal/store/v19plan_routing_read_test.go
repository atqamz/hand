package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadCanonicalV19CurrentPlanRoutingKeepsExactActiveAxes(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	input := canonicalV19PlanWriterInput("plan-1")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadCanonicalV19CurrentPlanRouting(context.Background(), fixture.Home, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != input.ID || got.Intent != input.Intent || got.Judgment != input.Judgment || got.PolicyRevisionID != input.PolicyRevisionID {
		t.Fatalf("exact active Plan route = %#v", got)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("Plan route read changed canonical DB: %v", err)
	}
	if _, err := ReadCanonicalV19CurrentPlanRouting(context.Background(), fixture.Home, "missing"); !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("missing Plan read = %v", err)
	}
	successor := canonicalV19PlanWriterInput("plan-2")
	successor.Intent = "execute"
	successor.Judgment = "substantial"
	successor.CreatedAt = "2026-09-04T08:01:00Z"
	if _, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: input.ID, Successor: successor, SupersededAt: "2026-09-04T08:00:59Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19CurrentPlanRouting(context.Background(), fixture.Home, input.ID); !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("superseded Plan route read = %v", err)
	}
	got, err = ReadCanonicalV19CurrentPlanRouting(context.Background(), fixture.Home, successor.ID)
	if err != nil || got.ID != successor.ID || got.Intent != successor.Intent || got.Judgment != successor.Judgment {
		t.Fatalf("successor Plan route = %#v, %v", got, err)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-24T00:00:00Z' WHERE id='project-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19CurrentPlanRouting(context.Background(), fixture.Home, successor.ID); !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("retired Project Plan route read = %v", err)
	}
}

func TestReadCanonicalV19CurrentPlanRoutingUsesExactPlanIndex(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN "+canonicalV19CurrentPlanRoutingQuery, "plan-1")
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
	if !strings.Contains(plan.String(), "SEARCH p USING ") || !strings.Contains(plan.String(), "SEARCH t USING ") ||
		!strings.Contains(plan.String(), "SEARCH pr USING ") || strings.Contains(plan.String(), "SCAN ") ||
		strings.Contains(plan.String(), "AUTOMATIC") {
		t.Fatalf("exact Plan route read scans history:\n%s", plan.String())
	}
}
