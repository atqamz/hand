package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCanonicalV19DecisionAnswerPersistsAuthorityWithoutDelivery(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	input := canonicalV19DecisionTestAnswer(question.ID)
	for range 2 {
		if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, input); err != nil {
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
	got, found, err := loadCanonicalV19DecisionAnswer(context.Background(), tx, input.DecisionID)
	if err != nil || !found || got != input {
		t.Fatalf("persisted Answer = %+v, found=%t, err=%v; want %+v", got, found, err, input)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"worker_input", "worker_input_answer_origin", "worker_input_acknowledgement", "worker_report_acknowledgement", "task_hold_resolution", "external_operation", "decision_closure"} {
		canonicalV19DecisionAssertCount(t, fixture.Home, "SELECT count(*) FROM "+table, 0)
	}
	for _, table := range []string{"task", "plan", "attempt"} {
		canonicalV19DecisionAssertCount(t, fixture.Home, "SELECT count(*) FROM "+table+" WHERE lifecycle='active'", 1)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 1)
	for name, change := range map[string]func(*CanonicalV19DecisionAnswerCreateInput){
		"ID":        func(in *CanonicalV19DecisionAnswerCreateInput) { in.ID += "-other" },
		"actor":     func(in *CanonicalV19DecisionAnswerCreateInput) { in.ActorRef += "-other" },
		"timestamp": func(in *CanonicalV19DecisionAnswerCreateInput) { in.AnsweredAt += "-other" },
		"answer": func(in *CanonicalV19DecisionAnswerCreateInput) {
			in.Answer += "different"
			in.AnswerDigest = canonicalV19SHA256([]byte(in.Answer))
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			change(&changed)
			if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, changed); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
				t.Fatalf("conflicting Answer = %v", err)
			}
		})
	}
	if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: question.AttemptID, Lifecycle: "failed", TerminalAt: "2026-09-21T10:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-2", question.PlanID)); err != nil {
		t.Fatal(err)
	}
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatalf("historical question replay: %v", err)
	}
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, input); err != nil {
		t.Fatalf("historical Answer replay: %v", err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision WHERE attempt_id='attempt-1'`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision WHERE attempt_id='attempt-2'`, 0)
}

func TestCanonicalV19DecisionAnswerRejectsUnsupportedAuthorityAndInvalidBytes(t *testing.T) {
	for name, change := range map[string]func(*CanonicalV19DecisionAnswerCreateInput){
		"missing ID":                   func(in *CanonicalV19DecisionAnswerCreateInput) { in.ID = "" },
		"missing Decision":             func(in *CanonicalV19DecisionAnswerCreateInput) { in.DecisionID = "" },
		"missing operator reference":   func(in *CanonicalV19DecisionAnswerCreateInput) { in.ActorRef = "" },
		"missing timestamp":            func(in *CanonicalV19DecisionAnswerCreateInput) { in.AnsweredAt = "" },
		"supervisor recommendation":    func(in *CanonicalV19DecisionAnswerCreateInput) { in.ActorKind = "supervisor" },
		"Worker claim":                 func(in *CanonicalV19DecisionAnswerCreateInput) { in.ActorKind = "worker" },
		"unimplemented machine policy": func(in *CanonicalV19DecisionAnswerCreateInput) { in.ActorKind = "authorized-machine" },
		"digest mismatch":              func(in *CanonicalV19DecisionAnswerCreateInput) { in.Answer += "changed" },
		"blank":                        func(in *CanonicalV19DecisionAnswerCreateInput) { in.Answer = " \n" },
		"invalid UTF-8":                func(in *CanonicalV19DecisionAnswerCreateInput) { in.Answer = "\xff" },
		"too large": func(in *CanonicalV19DecisionAnswerCreateInput) {
			in.Answer = strings.Repeat("x", canonicalV19DecisionMaxTextBytes+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := canonicalV19DecisionTestAnswer("decision-1")
			change(&input)
			if err := CreateCanonicalV19DecisionAnswer(context.Background(), t.TempDir(), input); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
				t.Fatalf("invalid authority = %v", err)
			}
		})
	}
}

func TestCanonicalV19DecisionAnswerRefusesRetryAndReplanTargets(t *testing.T) {
	for _, scope := range []string{"task", "plan", "attempt"} {
		t.Run(scope, func(t *testing.T) {
			fixture, attemptID := canonicalV19WorkerReportAttemptFixture(t)
			report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
				canonicalV19WorkerReportWitness(t, attemptID, "", "needs-decision: A1 request\n", "2026-09-21T09:00:00Z", nil))
			if err != nil {
				t.Fatal(err)
			}
			question := canonicalV19DecisionTestInput(scope)
			question.TriggeringWorkerReportID = report.ID
			if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
				t.Fatal(err)
			}
			if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
				AttemptID: attemptID, Lifecycle: "failed", TerminalAt: "2026-09-21T10:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-2", "plan-root")); err != nil {
				t.Fatal(err)
			}
			if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
				t.Fatalf("late Answer after retry = %v", err)
			}
			question.ID += "-new"
			if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
				t.Fatalf("new question from stale report = %v", err)
			}
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 0)
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input`, 0)
		})
	}
	t.Run("Plan replacement without a report", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		question := canonicalV19DecisionTestInput("plan")
		if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
			t.Fatal(err)
		}
		successor := canonicalV19PlanWriterInput("plan-2")
		if _, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
			PredecessorPlanID: "plan-root", Successor: successor, SupersededAt: "2026-09-21T10:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
			t.Fatalf("late Answer after replan = %v", err)
		}
		question.ID, question.PlanID = "decision-2", "plan-2"
		if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
			t.Fatal(err)
		}
		if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); err != nil {
			t.Fatal(err)
		}
		canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 1)
	})
}

func TestCanonicalV19DecisionAnswerIdentityCannotMoveToAnotherQuestion(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("task")
	for _, id := range []string{"decision-1", "decision-2"} {
		question.ID = id
		if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
			t.Fatal(err)
		}
	}
	answer := canonicalV19DecisionTestAnswer("decision-1")
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, answer); err != nil {
		t.Fatal(err)
	}
	answer.DecisionID = "decision-2"
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, answer); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
		t.Fatalf("reused Answer ID = %v", err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer WHERE decision_id='decision-1'`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer WHERE decision_id='decision-2'`, 0)
}

func TestCanonicalV19DecisionAnswerCompetesWithExactAttemptTermination(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	answerResult, terminalResult := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		answerResult <- CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID))
	}()
	go func() {
		<-start
		terminalResult <- TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
			AttemptID: question.AttemptID, Lifecycle: "failed", TerminalAt: "2026-09-21T10:00:00Z",
		})
	}()
	close(start)
	answerErr, terminalErr := <-answerResult, <-terminalResult
	if terminalErr != nil {
		t.Fatal(terminalErr)
	}
	wantAnswers := 0
	if answerErr == nil {
		wantAnswers = 1
	} else if !errors.Is(answerErr, ErrCanonicalV19DecisionNotCurrent) {
		t.Fatalf("racing Answer = %v", answerErr)
	}
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-2", question.PlanID)); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, wantAnswers)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision WHERE attempt_id='attempt-2'`, 0)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input`, 0)
	late := canonicalV19DecisionTestAnswer(question.ID)
	late.ID += "-late"
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, late); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) && !errors.Is(err, ErrCanonicalV19DecisionConflict) {
		t.Fatalf("late Answer after terminal winner = %v", err)
	}
}

func TestCanonicalV19DecisionConflictingAnswersHaveOneWinner(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, value := range []string{"A", "B"} {
		answer := canonicalV19DecisionTestAnswer(question.ID)
		answer.ID += "-" + value
		answer.Answer = value
		answer.AnswerDigest = canonicalV19SHA256([]byte(value))
		go func() {
			<-start
			results <- CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, answer)
		}()
	}
	close(start)
	wins := 0
	for range 2 {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrCanonicalV19DecisionConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("conflicting Answer winners = %d, want 1", wins)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 1)
}
