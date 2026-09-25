package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadCanonicalV19FleetSnapshotShowsOnlyCurrentOpenDecisions(t *testing.T) {
	ctx := context.Background()
	fixture := canonicalV19PlanWriterFixture(t, "")
	if _, err := CreateCanonicalV19Task(ctx, fixture.Home, CanonicalV19TaskCreateInput{
		ID: "task-2", ProjectID: "project-1", Goal: "task-2", GoalDigest: "digest-task-2",
		CreatedAt: "2026-09-05T02:59:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19RootPlan(ctx, fixture.Home, canonicalV19PlanWriterInput("plan-root")); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Attempt(ctx, fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	closed := CanonicalV19DecisionCreateInput{
		ID: "decision-closed", TaskID: "task-1", ScopeKind: "task",
		Question: "Should the closed question stay hidden?", CreatedAt: "2026-09-24T00:01:00Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, closed); err != nil {
		t.Fatal(err)
	}
	if err := CloseCanonicalV19Decision(ctx, fixture.Home, CanonicalV19DecisionCloseInput{
		DecisionID: closed.ID, Reason: "cancelled", ClosedAt: "2026-09-24T00:02:00Z", EvidenceDigest: "digest-closure",
	}); err != nil {
		t.Fatal(err)
	}
	answered := CanonicalV19DecisionCreateInput{
		ID: "decision-answered", TaskID: "task-1", ScopeKind: "task",
		Question: "Should the answered question stay hidden too?", CreatedAt: "2026-09-24T00:02:30Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, answered); err != nil {
		t.Fatal(err)
	}
	answerText := "yes, proceed"
	if err := CreateCanonicalV19DecisionAnswer(ctx, fixture.Home, CanonicalV19DecisionAnswerCreateInput{
		ID: "answer-1", DecisionID: answered.ID, Answer: answerText, AnswerDigest: canonicalV19SHA256([]byte(answerText)),
		ActorKind: "operator", ActorRef: "operator-1", AnsweredAt: "2026-09-24T00:02:45Z",
	}); err != nil {
		t.Fatal(err)
	}
	openDecision := CanonicalV19DecisionCreateInput{
		ID: "decision-open", TaskID: "task-1", ScopeKind: "task", ChoicesDigest: strings.Repeat("a", 64),
		Question: "Who can unblock this task?", CreatedAt: "2026-09-24T00:03:00Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, openDecision); err != nil {
		t.Fatal(err)
	}
	planScoped := CanonicalV19DecisionCreateInput{
		ID: "decision-plan", TaskID: "task-1", PlanID: "plan-root", ScopeKind: "plan",
		Question: "Does the Plan basis still hold?", CreatedAt: "2026-09-24T00:03:15Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, planScoped); err != nil {
		t.Fatal(err)
	}
	attemptScoped := CanonicalV19DecisionCreateInput{
		ID: "decision-attempt", TaskID: "task-1", PlanID: "plan-root", AttemptID: "attempt-1", ScopeKind: "attempt",
		Question: "Should this Attempt keep going?", CreatedAt: "2026-09-24T00:03:30Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, attemptScoped); err != nil {
		t.Fatal(err)
	}
	other := CanonicalV19DecisionCreateInput{
		ID: "decision-other-task", TaskID: "task-2", ScopeKind: "task",
		Question: "Does the other Task need review?", CreatedAt: "2026-09-24T00:04:00Z",
	}
	if err := CreateCanonicalV19Decision(ctx, fixture.Home, other); err != nil {
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
		t.Fatalf("Decision snapshot mutated canonical database: %v", err)
	}
	if len(snapshot.CurrentOpenDecisions) != 4 {
		t.Fatalf("current open Decisions = %#v", snapshot.CurrentOpenDecisions)
	}
	byID := make(map[string]CanonicalV19SnapshotDecision, len(snapshot.CurrentOpenDecisions))
	for _, decision := range snapshot.CurrentOpenDecisions {
		byID[decision.ID] = decision
	}
	if _, ok := byID[closed.ID]; ok {
		t.Fatalf("closed Decision unexpectedly still open: %#v", byID)
	}
	if _, ok := byID[answered.ID]; ok {
		t.Fatalf("answered Decision unexpectedly still open: %#v", byID)
	}
	if got := byID[openDecision.ID]; got.TaskID != "task-1" || got.ScopeKind != "task" || got.ChoicesDigest != openDecision.ChoicesDigest {
		t.Fatalf("task-scoped open Decision = %#v", got)
	}
	if got := byID[planScoped.ID]; got.TaskID != "task-1" || got.PlanID != "plan-root" || got.ScopeKind != "plan" {
		t.Fatalf("plan-scoped open Decision = %#v", got)
	}
	if got := byID[attemptScoped.ID]; got.TaskID != "task-1" || got.PlanID != "plan-root" || got.AttemptID != "attempt-1" || got.ScopeKind != "attempt" {
		t.Fatalf("attempt-scoped open Decision = %#v", got)
	}
	if got := byID[other.ID]; got.TaskID != "task-2" {
		t.Fatalf("other-Task open Decision = %#v", got)
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
	if err != nil || len(retired.CurrentOpenDecisions) != 4 {
		t.Fatalf("retired Project hid active Task Decisions: %#v, %v", retired.CurrentOpenDecisions, err)
	}
}

func TestReadCanonicalV19FleetSnapshotShowsOpenDecisionOnTerminalUnarchivedTask(t *testing.T) {
	ctx := context.Background()
	home := canonicalV19TaskHoldWriterFixture(t)
	open := CanonicalV19DecisionCreateInput{
		ID: "decision-open", TaskID: "task-1", ScopeKind: "task",
		Question: "Does abandonment still need a look?", CreatedAt: "2026-09-24T00:01:00Z",
	}
	if err := CreateCanonicalV19Decision(ctx, home, open); err != nil {
		t.Fatal(err)
	}
	if err := AbandonCanonicalV19Task(ctx, home, "task-1", "2026-09-24T00:02:00Z"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenDecisions) != 1 || snapshot.CurrentOpenDecisions[0].ID != open.ID {
		t.Fatalf("terminal-but-unarchived Task lost its open Decision: %#v", snapshot.CurrentOpenDecisions)
	}
}

func TestCanonicalV19SnapshotCurrentDecisionsQueryUsesTaskHistoryIndex(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + canonicalV19SnapshotCurrentDecisionsQuery)
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
	if !strings.Contains(plan.String(), "SEARCH d USING INDEX decision_task_history") ||
		strings.Contains(plan.String(), "SCAN d ") || strings.Contains(plan.String(), "SCAN da ") ||
		strings.Contains(plan.String(), "SCAN dc ") {
		t.Fatalf("current Decision query scans history instead of exact Task seeks: %s", plan.String())
	}
}
