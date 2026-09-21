package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestCanonicalV19DecisionClosureIsExactAndDoesNotReopen(t *testing.T) {
	for _, reason := range []string{"cancelled", "stale"} {
		t.Run(reason, func(t *testing.T) {
			fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
			question := canonicalV19DecisionTestInput("attempt")
			if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
				t.Fatal(err)
			}
			closure := CanonicalV19DecisionCloseInput{
				DecisionID: question.ID, Reason: reason, ClosedAt: "2026-09-21T10:00:00Z", EvidenceDigest: "explicit-closure-evidence",
			}
			if reason == "stale" {
				if err := CloseCanonicalV19Decision(context.Background(), fixture.Home, closure); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
					t.Fatalf("current question labelled stale = %v", err)
				}
				if err := TerminalizeCanonicalV19Attempt(context.Background(), fixture.Home, CanonicalV19AttemptTerminalizeInput{
					AttemptID: question.AttemptID, Lifecycle: "failed", TerminalAt: "2026-09-21T09:59:00Z",
				}); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := CloseCanonicalV19Decision(context.Background(), fixture.Home, closure); err != nil {
					t.Fatal(err)
				}
			}
			if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
				t.Fatalf("historical replay: %v", err)
			}
			if err := CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) {
				t.Fatalf("Answer after closure = %v", err)
			}
			closure.EvidenceDigest += "-different"
			if err := CloseCanonicalV19Decision(context.Background(), fixture.Home, closure); !errors.Is(err, ErrCanonicalV19DecisionConflict) {
				t.Fatalf("conflicting closure = %v", err)
			}
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision`, 1)
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_closure`, 1)
			canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 0)
		})
	}
}

func TestCanonicalV19DecisionAnswerAndClosureHaveOneWinner(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, action := range []func() error{
		func() error {
			return CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID))
		},
		func() error {
			return CloseCanonicalV19Decision(context.Background(), fixture.Home, CanonicalV19DecisionCloseInput{
				DecisionID: question.ID, Reason: "cancelled", ClosedAt: "2026-09-21T10:00:00Z", EvidenceDigest: "operator-cancelled",
			})
		},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- action()
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrCanonicalV19DecisionNotCurrent) && !errors.Is(err, ErrCanonicalV19DecisionConflict) {
			t.Fatalf("unexpected competing writer error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("terminal Decision winners = %d, want 1", wins)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT (SELECT count(*) FROM decision_answer)+(SELECT count(*) FROM decision_closure)`, 1)
}

func TestCanonicalV19DecisionConcurrentExactReplayConverges(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	for _, action := range []func() error{
		func() error { return CreateCanonicalV19Decision(context.Background(), fixture.Home, question) },
		func() error {
			return CreateCanonicalV19DecisionAnswer(context.Background(), fixture.Home, canonicalV19DecisionTestAnswer(question.ID))
		},
	} {
		start := make(chan struct{})
		results := make(chan error, 4)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- action()
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM decision_answer`, 1)
}

func TestCanonicalV19DecisionCanceledWriterDoesNotInferStaleness(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	question := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), fixture.Home, question); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CloseCanonicalV19Decision(ctx, fixture.Home, CanonicalV19DecisionCloseInput{
		DecisionID: question.ID, Reason: "stale", ClosedAt: "2026-09-21T10:00:00Z", EvidenceDigest: "unproven",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read became stale authority: %v", err)
	}
	if err := CreateCanonicalV19DecisionAnswer(ctx, fixture.Home, canonicalV19DecisionTestAnswer(question.ID)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Answer = %v", err)
	}
	canonicalV19DecisionAssertUnchangedWork(t, fixture.Home)
}
