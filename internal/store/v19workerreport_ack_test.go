package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateCanonicalV19WorkerReportAcknowledgementRejectsMissingTargetAndActor(t *testing.T) {
	fixture, _ := canonicalV19WorkerReportAttemptFixture(t)
	for name, input := range map[string]CanonicalV19WorkerReportAcknowledgementCreateInput{
		"missing target": {
			WorkerReportID: "missing-report", ActorKind: "supervisor",
			AcknowledgedAt: "2026-09-15T10:11:00Z", EvidenceDigest: "missing-report-evidence",
		},
		"unsupported actor": {
			WorkerReportID: "missing-report", ActorKind: "worker",
			AcknowledgedAt: "2026-09-15T10:11:00Z", EvidenceDigest: "wrong-actor-evidence",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CreateCanonicalV19WorkerReportAcknowledgement(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19WorkerReportAcknowledgementConflict) {
				t.Fatalf("WorkerReport acknowledgement error = %v, want %v", err, ErrCanonicalV19WorkerReportAcknowledgementConflict)
			}
		})
	}
}
