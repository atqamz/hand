package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19AnswerWorkerInputPersistsExactTypedOrigin(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	canonicalV19DecisionAnswerFixture(t, fixture.Home, launch, "decision-answer-1", "answer-1", "attempt", "")
	input := canonicalV19AnswerWorkerInputCreateInput(launch, "worker-input-answer-1", "answer-origin-1", "decision-answer-1", "answer-1")

	created, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != input.ID || created.AttemptID != launch.AttemptID || created.ExecutorBindingID != launch.BindingID ||
		created.Ordinal != 1 || created.Payload != input.Payload || created.PayloadDigest != input.PayloadDigest ||
		created.OriginKind != "answer" || created.CreatedAt != input.CreatedAt {
		t.Fatalf("Answer-origin WorkerInput = %#v", created)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var originKind, answerOriginID, decisionID, answerID, payloadType string
	if err := db.sql.QueryRow(`SELECT wi.origin_kind,wi.answer_origin_id,ao.decision_id,ao.answer_id,typeof(wi.payload)
		FROM worker_input wi JOIN worker_input_answer_origin ao ON ao.id=wi.answer_origin_id WHERE wi.id=?`, input.ID).Scan(
		&originKind, &answerOriginID, &decisionID, &answerID, &payloadType,
	); err != nil {
		t.Fatal(err)
	}
	if originKind != "answer" || answerOriginID != input.AnswerOriginID || decisionID != input.DecisionID ||
		answerID != input.AnswerID || payloadType != "blob" {
		t.Fatalf("persisted Answer-origin = %q/%q/%q/%q payload=%q",
			originKind, answerOriginID, decisionID, answerID, payloadType)
	}
}

func TestCreateCanonicalV19AnswerWorkerInputExactReplayConverges(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	canonicalV19DecisionAnswerFixture(t, fixture.Home, launch, "decision-answer-replay", "answer-replay", "attempt", "")
	input := canonicalV19AnswerWorkerInputCreateInput(launch, "worker-input-answer-replay", "answer-origin-replay", "decision-answer-replay", "answer-replay")

	first, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("replayed Answer-origin WorkerInput = %#v, want %#v", second, first)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var workerInputs, origins int
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input WHERE id=?`, input.ID).Scan(&workerInputs); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input_answer_origin WHERE id=?`, input.AnswerOriginID).Scan(&origins); err != nil {
		t.Fatal(err)
	}
	if workerInputs != 1 || origins != 1 {
		t.Fatalf("replay rows = WorkerInput %d / Answer-origin %d, want 1/1", workerInputs, origins)
	}
}

func TestCreateCanonicalV19AnswerWorkerInputIdentityDriftConflicts(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	canonicalV19DecisionAnswerFixture(t, fixture.Home, launch, "decision-answer-drift", "answer-drift", "attempt", "")
	input := canonicalV19AnswerWorkerInputCreateInput(launch, "worker-input-answer-drift", "answer-origin-drift", "decision-answer-drift", "answer-drift")
	if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	input.AnswerOriginID = "answer-origin-different"
	if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputConflict) {
		t.Fatalf("Answer-origin identity drift error = %v, want %v", err, ErrCanonicalV19WorkerInputConflict)
	}
}

func TestCreateCanonicalV19AnswerWorkerInputRefusesDecisionScopeMismatch(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	projectID := canonicalV19AttemptProjectID(t, fixture.Home, launch.AttemptID)
	if _, err := CreateCanonicalV19Task(context.Background(), fixture.Home, CanonicalV19TaskCreateInput{
		ID: "task-answer-other", ProjectID: projectID, Goal: "other task", GoalDigest: "digest-other-task",
		CreatedAt: "2026-09-08T08:30:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAnswerFixture(t, fixture.Home, launch, "decision-answer-wrong-scope", "answer-wrong-scope", "task", "task-answer-other")
	input := canonicalV19AnswerWorkerInputCreateInput(launch, "worker-input-answer-wrong-scope", "answer-origin-wrong-scope", "decision-answer-wrong-scope", "answer-wrong-scope")
	if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputConflict) {
		t.Fatalf("Answer-origin scope mismatch error = %v, want %v", err, ErrCanonicalV19WorkerInputConflict)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var workerInputs, origins int
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input WHERE id=?`, input.ID).Scan(&workerInputs); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT count(*) FROM worker_input_answer_origin WHERE id=?`, input.AnswerOriginID).Scan(&origins); err != nil {
		t.Fatal(err)
	}
	if workerInputs != 0 || origins != 0 {
		t.Fatalf("scope mismatch rows = WorkerInput %d / Answer-origin %d, want 0/0", workerInputs, origins)
	}
}

func TestCreateCanonicalV19AnswerWorkerInputRefusesTerminatedExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	canonicalV19DecisionAnswerFixture(t, fixture.Home, launch, "decision-answer-stale", "answer-stale", "attempt", "")
	interrupt := canonicalV19InterruptPrepareInput(launch, "operation-interrupt-answer-input")
	request, err := PrepareCanonicalV19Interrupt(context.Background(), fixture.Home, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitCanonicalV19Interrupt(context.Background(), fixture.Home, request.OperationID,
		"2026-09-08T08:31:00Z", "interrupt-answer-input-submitted"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19Interrupt(context.Background(), fixture.Home, CanonicalV19ExecutorInterruptedEvidence{
		OperationID: request.OperationID, ObservedAt: "2026-09-08T08:32:00Z", EvidenceDigest: "executor-answer-input-ceased",
	}); err != nil {
		t.Fatal(err)
	}

	input := canonicalV19AnswerWorkerInputCreateInput(launch, "worker-input-answer-stale", "answer-origin-stale", "decision-answer-stale", "answer-stale")
	input.CreatedAt = "2026-09-08T08:33:00Z"
	if _, err := CreateCanonicalV19AnswerWorkerInput(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerInputNotCurrent) {
		t.Fatalf("Answer-origin input after termination error = %v, want %v", err, ErrCanonicalV19WorkerInputNotCurrent)
	}
}

func canonicalV19AnswerWorkerInputCreateInput(
	launch CanonicalV19LaunchRequest,
	workerInputID, answerOriginID, decisionID, answerID string,
) CanonicalV19AnswerWorkerInputCreateInput {
	return CanonicalV19AnswerWorkerInputCreateInput{
		ID: workerInputID, AttemptID: launch.AttemptID, ExecutorBindingID: launch.BindingID,
		Payload: "decision answer instruction", PayloadDigest: "digest-decision-answer-instruction",
		AnswerOriginID: answerOriginID, DecisionID: decisionID, AnswerID: answerID,
		CreatedAt: "2026-09-08T08:30:00Z",
	}
}

func canonicalV19DecisionAnswerFixture(
	t *testing.T,
	homeDir string,
	launch CanonicalV19LaunchRequest,
	decisionID, answerID, scopeKind, scopeTaskID string,
) {
	t.Helper()
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var planID, taskID string
	if err := db.QueryRow(`SELECT a.plan_id,p.task_id FROM attempt a JOIN plan p ON p.id=a.plan_id WHERE a.id=?`,
		launch.AttemptID).Scan(&planID, &taskID); err != nil {
		t.Fatal(err)
	}
	var decisionPlanID, decisionAttemptID any
	switch scopeKind {
	case "attempt":
		decisionPlanID, decisionAttemptID = planID, launch.AttemptID
	case "plan":
		decisionPlanID = planID
		decisionAttemptID = nil
	case "task":
		decisionPlanID, decisionAttemptID = nil, nil
		if scopeTaskID != "" {
			taskID = scopeTaskID
		}
	default:
		t.Fatalf("unsupported test Decision scope %q", scopeKind)
	}
	if _, err := db.Exec(`INSERT INTO decision(
		id,task_id,plan_id,attempt_id,scope_kind,triggering_worker_report_id,question,choices_digest,created_at
	) VALUES(?,?,?,?,?,NULL,?,'',?)`, decisionID, taskID, decisionPlanID, decisionAttemptID, scopeKind,
		"question "+decisionID, "2026-09-08T08:20:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO decision_answer(
		id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at
	) VALUES(?,?,?,?, 'operator','',?)`, answerID, decisionID, "approved", "digest-"+answerID,
		"2026-09-08T08:25:00Z"); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19AttemptProjectID(t *testing.T, homeDir, attemptID string) string {
	t.Helper()
	db, err := openReadOnly(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var projectID string
	if err := db.sql.QueryRow(`SELECT t.project_id FROM attempt a JOIN plan p ON p.id=a.plan_id JOIN task t ON t.id=p.task_id WHERE a.id=?`,
		attemptID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	return projectID
}
