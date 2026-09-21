package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCanonicalV19DecisionRecordsExactScopesWithoutImplicitAuthority(t *testing.T) {
	for _, scope := range []string{"task", "plan", "attempt"} {
		t.Run(scope, func(t *testing.T) {
			fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
			report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
				canonicalV19WorkerReportWitness(t, attemptID, "", "needs-decision: choose approach\n", "2026-09-21T09:00:00Z", nil))
			if err != nil {
				t.Fatal(err)
			}
			input := canonicalV19DecisionTestInput(scope)
			input.TriggeringWorkerReportID = report.ID
			for range 2 {
				if err := CreateCanonicalV19Decision(nil, fixture.Home, input); err != nil {
					t.Fatal(err)
				}
			}
			db, err := openReadOnly(fixture.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tx, err := db.sql.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			got, found, err := loadCanonicalV19Decision(context.Background(), tx, input.ID)
			if err != nil || !found || got != input {
				t.Fatalf("persisted Decision = %+v, found=%t, err=%v; want %+v", got, found, err, input)
			}
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision`, 1)
			canonicalV19DecisionAssertUnchangedWork(t, fixture.Home)
			input.Question += " changed"
			if err := CreateCanonicalV19Decision(nil, fixture.Home, input); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
				t.Fatalf("changed replay = %v", err)
			}
		})
	}
}

func TestCanonicalV19DecisionRejectsInvalidScopeAndQuestionBeforeOpeningHome(t *testing.T) {
	for name, change := range map[string]func(*CanonicalV19DecisionCreateInput){
		"missing ID":           func(in *CanonicalV19DecisionCreateInput) { in.ID = "" },
		"missing Task":         func(in *CanonicalV19DecisionCreateInput) { in.TaskID = "" },
		"missing timestamp":    func(in *CanonicalV19DecisionCreateInput) { in.CreatedAt = "" },
		"unknown scope":        func(in *CanonicalV19DecisionCreateInput) { in.ScopeKind = "executor" },
		"Task with Plan":       func(in *CanonicalV19DecisionCreateInput) { in.ScopeKind = "task" },
		"Plan with Attempt":    func(in *CanonicalV19DecisionCreateInput) { in.ScopeKind = "plan" },
		"Attempt without Plan": func(in *CanonicalV19DecisionCreateInput) { in.PlanID = "" },
		"blank question":       func(in *CanonicalV19DecisionCreateInput) { in.Question = " \n\t" },
		"invalid UTF-8":        func(in *CanonicalV19DecisionCreateInput) { in.Question = "\xff" },
		"oversized question": func(in *CanonicalV19DecisionCreateInput) {
			in.Question = strings.Repeat("x", canonicalV19DecisionMaxTextBytes+1)
		},
		"invalid choices digest": func(in *CanonicalV19DecisionCreateInput) { in.ChoicesDigest = "not-a-digest" },
	} {
		t.Run(name, func(t *testing.T) {
			input := canonicalV19DecisionTestInput("attempt")
			change(&input)
			if err := CreateCanonicalV19Decision(nil, t.TempDir(), input); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
				t.Fatalf("invalid input = %v", err)
			}
		})
	}
}

func TestCanonicalV19DecisionRejectsForeignAndMissingTriggerLineage(t *testing.T) {
	fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
	report, err := IngestCanonicalV19WorkerReport(nil, fixture.Home,
		canonicalV19WorkerReportWitness(t, attemptID, "", "needs-decision: original\n", "2026-09-21T09:00:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	// A second real Task is still current but does not own this report.
	task := CanonicalV19TaskCreateInput{ID: "task-2", ProjectID: "project-1", Goal: "Separate work", GoalDigest: "digest-task-2", CreatedAt: "2026-09-21T09:00:00Z"}
	if _, err := CreateCanonicalV19Task(nil, fixture.Home, task); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"task", "plan", "attempt"} {
		input := canonicalV19DecisionTestInput(scope)
		input.TriggeringWorkerReportID = "missing-report"
		if err := CreateCanonicalV19Decision(nil, fixture.Home, input); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
			t.Fatalf("%s missing trigger = %v", scope, err)
		}
	}
	input := canonicalV19DecisionTestInput("task")
	input.TaskID, input.TriggeringWorkerReportID = task.ID, report.ID
	if err := CreateCanonicalV19Decision(nil, fixture.Home, input); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
		t.Fatalf("foreign Task report = %v", err)
	}
	input = canonicalV19DecisionTestInput("attempt")
	input.TaskID = task.ID
	if err := CreateCanonicalV19Decision(nil, fixture.Home, input); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
		t.Fatalf("foreign Task/Plan lineage = %v", err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision`, 0)
}

func TestCanonicalV19DecisionAndHoldRemainIndependent(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	hold := canonicalV19TaskHoldWriterInput("standalone-hold")
	if _, err := CreateCanonicalV19TaskHold(nil, fixture.Home, hold); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision`, 0)
	question := canonicalV19DecisionTestInput("task")
	if err := CreateCanonicalV19Decision(nil, fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	hold.ID, hold.DecisionID = "linked-hold", question.ID
	if _, err := CreateCanonicalV19TaskHold(nil, fixture.Home, hold); err != nil {
		t.Fatal(err)
	}
	answer := canonicalV19DecisionTestAnswer(question.ID)
	if err := CreateCanonicalV19DecisionAnswer(nil, fixture.Home, answer); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM task_hold`, 2)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM task_hold_decision`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM task_hold_resolution`, 0)
}

func TestCanonicalV19DecisionPermitsExplicitPostArchiveTaskQuestion(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	db := canonicalV19TaskArchiveOpen(t, home)
	canonicalV19InsertTaskArchive(t, db, "task-1")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19DecisionTestInput("task")
	input.Question = "May the retained historical artifact be published?"
	if err := CreateCanonicalV19Decision(nil, home, input); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM decision d
		WHERE NOT EXISTS (SELECT 1 FROM decision_answer a WHERE a.decision_id=d.id)
		AND NOT EXISTS (SELECT 1 FROM decision_closure c WHERE c.decision_id=d.id)`, 1)
	if err := CreateCanonicalV19DecisionAnswer(nil, home, canonicalV19DecisionTestAnswer(input.ID)); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task_archive`, 1)
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE lifecycle='satisfied'`, 1)
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM plan WHERE lifecycle='satisfied'`, 1)
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM attempt WHERE lifecycle='completed'`, 1)
}

func canonicalV19DecisionTestInput(scope string) CanonicalV19DecisionCreateInput {
	input := CanonicalV19DecisionCreateInput{
		ID: "decision-1", TaskID: "task-1", ScopeKind: scope,
		Question: "Choose A or B?\nKeep exact evidence. ", ChoicesDigest: canonicalV19SHA256([]byte("A\nB")), CreatedAt: "2026-09-21T09:01:00Z",
	}
	if scope != "task" {
		input.PlanID = "plan-root"
	}
	if scope == "attempt" {
		input.AttemptID = "attempt-1"
	}
	return input
}

func canonicalV19DecisionTestAnswer(decisionID string) CanonicalV19DecisionAnswerCreateInput {
	answer := "Use A.\nPreserve operator whitespace. "
	return CanonicalV19DecisionAnswerCreateInput{
		ID: "answer-" + decisionID, DecisionID: decisionID, Answer: answer, AnswerDigest: canonicalV19SHA256([]byte(answer)),
		ActorKind: "operator", ActorRef: "operator:explicit-cli-input", AnsweredAt: "2026-09-21T09:02:00Z",
	}
}

func canonicalV19DecisionAssertCount(t *testing.T, home, query string, want int) {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got int
	if err := db.sql.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}

func canonicalV19DecisionAssertUnchangedWork(t *testing.T, home string) {
	t.Helper()
	for _, table := range []string{"decision_answer", "decision_closure", "task_hold", "worker_input", "worker_input_acknowledgement", "worker_report_acknowledgement", "external_operation"} {
		canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM "+table, 0)
	}
	for _, table := range []string{"task", "plan", "attempt"} {
		canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM "+table+" WHERE lifecycle='active' AND terminal_at=''", 1)
	}
}
