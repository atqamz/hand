package store

import (
	"context"
	"errors"
	"testing"
)

func TestCanonicalV19DecisionDeliveryRefusesSuccessorOfTriggeringReport(t *testing.T) {
	for _, scope := range []string{"task", "plan", "attempt"} {
		t.Run(scope, func(t *testing.T) {
			fixture, _, first := canonicalV19ExecutorBindingFixture(t)
			report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
				canonicalV19WorkerReportWitness(t, first.AttemptID, first.BindingID, "needs-decision: original executor\n", "2026-09-21T09:00:00Z", nil))
			if err != nil {
				t.Fatal(err)
			}
			question := canonicalV19DecisionTestInput(scope)
			question.TriggeringWorkerReportID = report.ID
			if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
				t.Fatal(err)
			}
			answer := canonicalV19DecisionTestAnswer(question.ID)
			if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, answer); err != nil {
				t.Fatal(err)
			}
			canonicalV19DecisionTestStopExecutor(t, fixture.Home, first)
			second := canonicalV19DecisionTestRetryExecutor(t, fixture.Home, first.AttemptID)
			// The successor is positively current and accepts its own ordinary input.
			if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home,
				canonicalV19WorkerInputCreateInput(second, "input-control", "current control", "digest-control")); err != nil {
				t.Fatalf("successor positive control: %v", err)
			}
			input := canonicalV19AnswerWorkerInputCreateInput(second, "input-late-answer", "origin-late-answer", question.ID, answer.ID)
			if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputConflict) {
				t.Fatalf("trigger-scoped Answer retargeted successor: %v", err)
			}
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input_answer_origin`, 0)
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input`, 1)
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 1)
		})
	}
}

func TestCanonicalV19DecisionDeliveryReplaysWithoutWakeOrAcknowledgement(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, launch.AttemptID, launch.BindingID, "needs-decision: current executor\n", "2026-09-21T09:00:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	var delivered []CanonicalV19AnswerWorkerInputCreateInput
	for _, scope := range []string{"task", "plan", "attempt"} {
		question := canonicalV19DecisionTestInput(scope)
		question.ID = "decision-" + scope
		question.TriggeringWorkerReportID = report.ID
		if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
			t.Fatal(err)
		}
		answer := canonicalV19DecisionTestAnswer(question.ID)
		if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, answer); err != nil {
			t.Fatal(err)
		}
		input := canonicalV19AnswerWorkerInputCreateInput(launch, "input-"+scope, "origin-"+scope, question.ID, answer.ID)
		input.Payload, input.PayloadDigest = answer.Answer, answer.AnswerDigest
		first, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
		if err != nil {
			t.Fatal(err)
		}
		// A fresh writer/connection after input commit models retry before any wake.
		replayed, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
		if err != nil || replayed != first {
			t.Fatalf("input replay = %+v, %v; want %+v", replayed, err, first)
		}
		delivered = append(delivered, input)
		input.ID += "-duplicate"
		input.AnswerOriginID += "-duplicate"
		if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputConflict) {
			t.Fatalf("second input identity for same Answer = %v", err)
		}
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input_answer_origin`, 3)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input`, 3)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input_acknowledgement`, 0)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_wake_operation`, 0)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt WHERE lifecycle='active'`, 1)
	canonicalV19DecisionTestStopExecutor(t, fixture.Home, launch)
	canonicalV19DecisionTestRetryExecutor(t, fixture.Home, launch.AttemptID)
	for _, input := range delivered {
		replayed, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
		if err != nil || replayed.AttemptID != launch.AttemptID || replayed.ExecutorBindingID != launch.BindingID {
			t.Fatalf("historical input replay retargeted: %+v, %v", replayed, err)
		}
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM worker_input WHERE attempt_id='attempt-2'`, 0)
}

func TestCanonicalV19DecisionAnswerRefusesTerminatedTriggerExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	report, err := IngestCanonicalV19WorkerReport(context.Background(), fixture.Home,
		canonicalV19WorkerReportWitness(t, launch.AttemptID, launch.BindingID, "needs-decision: original\n", "2026-09-21T09:00:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	question := canonicalV19DecisionTestInput("task")
	question.TriggeringWorkerReportID = report.ID
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionTestStopExecutor(t, fixture.Home, launch)
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
		t.Fatalf("Answer after exact executor termination = %v", err)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM attempt WHERE lifecycle='active'`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 0)
}

func canonicalV19DecisionTestStopExecutor(t *testing.T, home string, launch CanonicalV19LaunchRequest) {
	t.Helper()
	request, err := PrepareCanonicalV19Interrupt(context.Background(), home, canonicalV19InterruptPrepareInput(launch, "interrupt-"+launch.BindingID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), home, request.OperationID, "2026-09-21T10:00:00Z", "submitted"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-21T10:01:00Z", EvidenceDigest: "ceased-exact-executor",
	}); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19DecisionTestRetryExecutor(t *testing.T, home, predecessorID string) CanonicalV19LaunchRequest {
	t.Helper()
	if err := TerminalizeCanonicalV19Attempt(context.Background(), home, CanonicalV19AttemptTerminalizeInput{
		AttemptID: predecessorID, Lifecycle: "interrupted", TerminalAt: "2026-09-21T10:02:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-2", "plan-root")); err != nil {
		t.Fatal(err)
	}
	create := canonicalV19WorktreeCreatePrepareInput(home, "worktree-create-2", "worktree-binding-2")
	create.AttemptID = "attempt-2"
	worktree, err := PrepareCanonicalV19WorktreeCreate(context.Background(), home, create)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(context.Background(), home, canonicalV19WorktreeBindingEvidence(worktree, "physical-2")); err != nil {
		t.Fatal(err)
	}
	acquire := canonicalV19SessionAcquirePrepareInput(worktree, "session-acquire-2", "session-binding-2")
	acquire.RequestedProviderSessionKey = "provider-session-2"
	session, err := PrepareCanonicalV19SessionAcquire(context.Background(), home, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: "provider-session-2", EstablishedAt: "2026-09-21T10:03:00Z", EvidenceDigest: "session-established-2",
	}); err != nil {
		t.Fatal(err)
	}
	launch, err := PrepareCanonicalV19Launch(context.Background(), home, canonicalV19LaunchPrepareInput(worktree, session, "launch-2", "executor-binding-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(context.Background(), home, CanonicalV19ExecutorBindingEvidence{
		OperationID: launch.OperationID, ProviderExecutorKey: "provider-executor-2", EstablishedAt: "2026-09-21T10:04:00Z", EvidenceDigest: "executor-established-2",
	}); err != nil {
		t.Fatal(err)
	}
	return launch
}
