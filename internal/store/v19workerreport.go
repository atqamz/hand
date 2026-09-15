package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrCanonicalV19WorkerReportConflict = errors.New("canonical v19 WorkerReport witness conflict")

var ErrCanonicalV19WorkerReportIncomplete = errors.New("canonical v19 WorkerReport record is incomplete")

var ErrCanonicalV19WorkerReportNotCurrent = errors.New("canonical v19 WorkerReport is not current")

var ErrCanonicalV19WorkerReportWitnessUnproven = errors.New("canonical v19 WorkerReport append witness is unproven")

// Only an exact source/provider observation may create this package-private token.
// No production source adapter creates one yet.
type canonicalV19WorkerReportSourceAttestation struct {
	witnessDigest string
}

// CanonicalV19WorkerReportPredecessor identifies the exact persisted boundary
// that an append witness continues. It is mechanism evidence, not a cursor.
type CanonicalV19WorkerReportPredecessor struct {
	WorkerReportID     string
	SourcePrefixDigest string
	SourceEndOffset    int64
}

// CanonicalV19WorkerReportAppendWitness carries one complete predecessor-linked
// source record. Production witness creation belongs to the provider boundary.
type CanonicalV19WorkerReportAppendWitness struct {
	AttemptID            string
	ExecutorBindingID    string
	Predecessor          *CanonicalV19WorkerReportPredecessor
	AppendedRecord       []byte
	AppendedRecordDigest string
	SourceEndOffset      int64
	CreatedAt            string
	sourceAttestation    *canonicalV19WorkerReportSourceAttestation
}

// CanonicalV19WorkerReport is one immutable Attempt-scoped Worker claim.
type CanonicalV19WorkerReport struct {
	ID                 string
	AttemptID          string
	ExecutorBindingID  string
	SourcePrefixDigest string
	SourceEndOffset    int64
	ReportState        string
	Note               string
	CreatedAt          string
}

// IngestCanonicalV19WorkerReport validates and persists one exact append witness.
// It never reads a mutable report file or acknowledges the resulting report.
func IngestCanonicalV19WorkerReport(
	ctx context.Context,
	homeDir string,
	witness CanonicalV19WorkerReportAppendWitness,
) (CanonicalV19WorkerReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !canonicalV19WorkerReportSourceAttestationMatches(witness) {
		return CanonicalV19WorkerReport{}, ErrCanonicalV19WorkerReportWitnessUnproven
	}
	report, err := canonicalV19WorkerReportFromAppendWitness(witness)
	if err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	unlock, err := lockCanonicalV19WorkerReportCheckpoint(homeDir, witness.AttemptID)
	if err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	defer unlock()

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return CanonicalV19WorkerReport{}, canonicalV19WorkerReportWriteError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return CanonicalV19WorkerReport{}, fmt.Errorf("ingest canonical v19 WorkerReport: %w", err)
	}
	if err := requireCanonicalV19WorkerReportPredecessorEvidence(ctx, tx, witness); err != nil {
		return CanonicalV19WorkerReport{}, err
	}

	if existing, found, err := loadCanonicalV19WorkerReportByID(ctx, tx, report.ID); err != nil {
		return CanonicalV19WorkerReport{}, err
	} else if found {
		if canonicalV19WorkerReportMatchesSourceEvidence(existing, report) {
			return existing, nil
		}
		return CanonicalV19WorkerReport{}, fmt.Errorf("%w: WorkerReport %q has different immutable evidence",
			ErrCanonicalV19WorkerReportConflict, report.ID)
	}
	if existing, found, err := loadCanonicalV19WorkerReportBySourcePrefix(ctx, tx, report.AttemptID, report.SourcePrefixDigest); err != nil {
		return CanonicalV19WorkerReport{}, err
	} else if found {
		if canonicalV19WorkerReportMatchesSourceEvidence(existing, report) {
			return existing, nil
		}
		return CanonicalV19WorkerReport{}, fmt.Errorf("%w: Attempt %q source prefix %q has different immutable evidence",
			ErrCanonicalV19WorkerReportConflict, report.AttemptID, report.SourcePrefixDigest)
	}

	if err := requireCanonicalV19WorkerReportCurrent(ctx, tx, report); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := requireCanonicalV19WorkerReportAppendTail(ctx, tx, homeDir, witness); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := markCanonicalV19WorkerReportCheckpointUncertain(homeDir, witness.AttemptID); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := injectCanonicalV19WorkerReportCheckpointFault(
		ctx, canonicalV19WorkerReportCheckpointAfterUncertain,
	); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := insertCanonicalV19WorkerReport(ctx, tx, report); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return CanonicalV19WorkerReport{}, canonicalV19WorkerReportWriteError("commit writer", err)
	}
	committed = true
	if err := injectCanonicalV19WorkerReportCheckpointFault(
		ctx, canonicalV19WorkerReportCheckpointAfterCommit,
	); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	if err := writeCanonicalV19WorkerReportCheckpointTail(homeDir, report); err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	return report, nil
}

// ReplayCanonicalV19WorkerReports validates a complete witness chain from its
// root. Existing exact rows converge; truncation or rewritten history conflicts.
func ReplayCanonicalV19WorkerReports(
	ctx context.Context,
	homeDir string,
	attemptID string,
	witnesses []CanonicalV19WorkerReportAppendWitness,
) ([]CanonicalV19WorkerReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if attemptID == "" {
		return nil, fmt.Errorf("replay canonical v19 WorkerReports: Attempt ID is empty")
	}
	reports := make([]CanonicalV19WorkerReport, 0, len(witnesses))
	for i, witness := range witnesses {
		if !canonicalV19WorkerReportSourceAttestationMatches(witness) {
			return nil, fmt.Errorf("%w: full replay witness %d is not attested",
				ErrCanonicalV19WorkerReportWitnessUnproven, i)
		}
		if witness.AttemptID != attemptID {
			return nil, fmt.Errorf("%w: replay witness %d belongs to Attempt %q, want %q",
				ErrCanonicalV19WorkerReportConflict, i, witness.AttemptID, attemptID)
		}
		if i == 0 {
			if witness.Predecessor != nil {
				return nil, fmt.Errorf("%w: full replay does not start at the source root",
					ErrCanonicalV19WorkerReportConflict)
			}
		} else if !canonicalV19WorkerReportPredecessorMatches(witness.Predecessor, reports[i-1]) {
			return nil, fmt.Errorf("%w: replay witness %d does not continue its exact predecessor",
				ErrCanonicalV19WorkerReportConflict, i)
		}
		report, err := canonicalV19WorkerReportFromAppendWitness(witness)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	unlock, err := lockCanonicalV19WorkerReportCheckpoint(homeDir, attemptID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, canonicalV19WorkerReportWriteError("begin replay writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("replay canonical v19 WorkerReports: %w", err)
	}

	existing, err := loadCanonicalV19WorkerReportsByAttempt(ctx, tx, attemptID)
	if err != nil {
		return nil, err
	}
	shared := len(existing)
	if len(reports) < shared {
		shared = len(reports)
	}
	for i := 0; i < shared; i++ {
		if !canonicalV19WorkerReportMatchesSourceEvidence(existing[i], reports[i]) {
			return nil, fmt.Errorf("%w: full replay differs at WorkerReport %d",
				ErrCanonicalV19WorkerReportConflict, i)
		}
		reports[i] = existing[i]
	}
	if len(existing) > len(reports) {
		return nil, fmt.Errorf("%w: full replay ends after %d reports but %d immutable reports exist",
			ErrCanonicalV19WorkerReportConflict, len(reports), len(existing))
	}
	currentBindings := make(map[string]bool)
	for i := len(existing); i < len(reports); i++ {
		if !currentBindings[reports[i].ExecutorBindingID] {
			if err := requireCanonicalV19WorkerReportCurrent(ctx, tx, reports[i]); err != nil {
				return nil, err
			}
			currentBindings[reports[i].ExecutorBindingID] = true
		}
	}
	if err := markCanonicalV19WorkerReportCheckpointUncertain(homeDir, attemptID); err != nil {
		return nil, err
	}
	if err := injectCanonicalV19WorkerReportCheckpointFault(
		ctx, canonicalV19WorkerReportCheckpointAfterUncertain,
	); err != nil {
		return nil, err
	}
	for i := len(existing); i < len(reports); i++ {
		if err := insertCanonicalV19WorkerReport(ctx, tx, reports[i]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, canonicalV19WorkerReportWriteError("commit replay writer", err)
	}
	committed = true
	if err := injectCanonicalV19WorkerReportCheckpointFault(
		ctx, canonicalV19WorkerReportCheckpointAfterCommit,
	); err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		if err := removeCanonicalV19WorkerReportCheckpoint(homeDir, attemptID); err != nil {
			return nil, err
		}
	} else if err := writeCanonicalV19WorkerReportCheckpointTail(homeDir, reports[len(reports)-1]); err != nil {
		return nil, err
	}
	return reports, nil
}

func canonicalV19WorkerReportFromAppendWitness(
	witness CanonicalV19WorkerReportAppendWitness,
) (CanonicalV19WorkerReport, error) {
	if witness.AttemptID == "" {
		return CanonicalV19WorkerReport{}, fmt.Errorf("ingest canonical v19 WorkerReport: Attempt ID is empty")
	}
	if witness.AppendedRecordDigest == "" || witness.CreatedAt == "" {
		return CanonicalV19WorkerReport{}, fmt.Errorf("ingest canonical v19 WorkerReport: record digest and created_at are required")
	}
	predecessorDigest := ""
	var predecessorOffset int64
	if witness.Predecessor != nil {
		if witness.Predecessor.WorkerReportID == "" || witness.Predecessor.SourcePrefixDigest == "" ||
			witness.Predecessor.SourceEndOffset <= 0 {
			return CanonicalV19WorkerReport{}, fmt.Errorf("%w: predecessor witness is incomplete",
				ErrCanonicalV19WorkerReportConflict)
		}
		predecessorDigest = witness.Predecessor.SourcePrefixDigest
		predecessorOffset = witness.Predecessor.SourceEndOffset
	}
	state, note, err := parseCanonicalV19WorkerReportRecord(witness.AppendedRecord)
	if err != nil {
		return CanonicalV19WorkerReport{}, err
	}
	recordDigest := sha256.Sum256(witness.AppendedRecord)
	if got := hex.EncodeToString(recordDigest[:]); got != witness.AppendedRecordDigest {
		return CanonicalV19WorkerReport{}, fmt.Errorf("%w: appended record digest=%q, want %q",
			ErrCanonicalV19WorkerReportConflict, witness.AppendedRecordDigest, got)
	}
	wantEndOffset := predecessorOffset + int64(len(witness.AppendedRecord))
	if witness.SourceEndOffset != wantEndOffset {
		return CanonicalV19WorkerReport{}, fmt.Errorf("%w: source end offset=%d, want %d",
			ErrCanonicalV19WorkerReportConflict, witness.SourceEndOffset, wantEndOffset)
	}
	sourcePrefixDigest := canonicalV19WorkerReportSourcePrefixDigest(
		predecessorDigest, predecessorOffset, witness.AppendedRecordDigest,
		witness.AppendedRecord, witness.SourceEndOffset,
	)
	return CanonicalV19WorkerReport{
		ID:        canonicalV19WorkerReportID(witness.AttemptID, sourcePrefixDigest),
		AttemptID: witness.AttemptID, ExecutorBindingID: witness.ExecutorBindingID,
		SourcePrefixDigest: sourcePrefixDigest, SourceEndOffset: witness.SourceEndOffset,
		ReportState: state, Note: note, CreatedAt: witness.CreatedAt,
	}, nil
}

func parseCanonicalV19WorkerReportRecord(record []byte) (string, string, error) {
	if len(record) == 0 || record[len(record)-1] != '\n' {
		return "", "", ErrCanonicalV19WorkerReportIncomplete
	}
	line := record[:len(record)-1]
	if bytes.ContainsRune(line, '\n') {
		return "", "", fmt.Errorf("%w: append witness contains multiple records",
			ErrCanonicalV19WorkerReportConflict)
	}
	prefix, note, ok := strings.Cut(string(line), ":")
	if !ok {
		return "", "", fmt.Errorf("%w: report record has no state delimiter",
			ErrCanonicalV19WorkerReportConflict)
	}
	prefix = strings.TrimSpace(prefix)
	note = strings.TrimSpace(note)
	switch prefix {
	case "working", "paused", "blocked", "needs-decision", "done", "failed":
		return prefix, note, nil
	default:
		return "", "", fmt.Errorf("%w: unsupported report state %q",
			ErrCanonicalV19WorkerReportConflict, prefix)
	}
}

func canonicalV19WorkerReportSourcePrefixDigest(
	predecessorDigest string,
	predecessorOffset int64,
	recordDigest string,
	record []byte,
	sourceEndOffset int64,
) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worker-report-source-prefix:v1")
	writeCanonicalV19DigestField(hash, "predecessor_source_prefix_digest", predecessorDigest)
	writeCanonicalV19DigestField(hash, "predecessor_source_end_offset", strconv.FormatInt(predecessorOffset, 10))
	writeCanonicalV19DigestField(hash, "appended_record_digest", recordDigest)
	writeCanonicalV19DigestField(hash, "appended_record", string(record))
	writeCanonicalV19DigestField(hash, "source_end_offset", strconv.FormatInt(sourceEndOffset, 10))
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19WorkerReportAppendWitnessDigest(witness CanonicalV19WorkerReportAppendWitness) string {
	predecessorID := ""
	predecessorDigest := ""
	var predecessorOffset int64
	if witness.Predecessor != nil {
		predecessorID = witness.Predecessor.WorkerReportID
		predecessorDigest = witness.Predecessor.SourcePrefixDigest
		predecessorOffset = witness.Predecessor.SourceEndOffset
	}
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worker-report-append-witness:v1")
	writeCanonicalV19DigestField(hash, "attempt_id", witness.AttemptID)
	writeCanonicalV19DigestField(hash, "executor_binding_id", witness.ExecutorBindingID)
	writeCanonicalV19DigestField(hash, "predecessor_worker_report_id", predecessorID)
	writeCanonicalV19DigestField(hash, "predecessor_source_prefix_digest", predecessorDigest)
	writeCanonicalV19DigestField(hash, "predecessor_source_end_offset", strconv.FormatInt(predecessorOffset, 10))
	writeCanonicalV19DigestField(hash, "appended_record_digest", witness.AppendedRecordDigest)
	writeCanonicalV19DigestField(hash, "appended_record", string(witness.AppendedRecord))
	writeCanonicalV19DigestField(hash, "source_end_offset", strconv.FormatInt(witness.SourceEndOffset, 10))
	writeCanonicalV19DigestField(hash, "created_at", witness.CreatedAt)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19WorkerReportSourceAttestationMatches(witness CanonicalV19WorkerReportAppendWitness) bool {
	return witness.sourceAttestation != nil &&
		witness.sourceAttestation.witnessDigest == canonicalV19WorkerReportAppendWitnessDigest(witness)
}

func canonicalV19WorkerReportID(attemptID, sourcePrefixDigest string) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worker-report-id:v1")
	writeCanonicalV19DigestField(hash, "attempt_id", attemptID)
	writeCanonicalV19DigestField(hash, "source_prefix_digest", sourcePrefixDigest)
	return "worker-report-" + hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19WorkerReportMatchesSourceEvidence(left, right CanonicalV19WorkerReport) bool {
	return left.ID == right.ID && left.AttemptID == right.AttemptID &&
		left.ExecutorBindingID == right.ExecutorBindingID &&
		left.SourcePrefixDigest == right.SourcePrefixDigest &&
		left.SourceEndOffset == right.SourceEndOffset &&
		left.ReportState == right.ReportState && left.Note == right.Note
}

func requireCanonicalV19WorkerReportPredecessorEvidence(
	ctx context.Context,
	tx *sql.Tx,
	witness CanonicalV19WorkerReportAppendWitness,
) error {
	if witness.Predecessor == nil {
		return nil
	}
	predecessor, found, err := loadCanonicalV19WorkerReportByID(ctx, tx, witness.Predecessor.WorkerReportID)
	if err != nil {
		return err
	}
	if !found || predecessor.AttemptID != witness.AttemptID ||
		predecessor.SourcePrefixDigest != witness.Predecessor.SourcePrefixDigest ||
		predecessor.SourceEndOffset != witness.Predecessor.SourceEndOffset {
		return fmt.Errorf("%w: exact predecessor witness is missing or changed",
			ErrCanonicalV19WorkerReportConflict)
	}
	return nil
}

func requireCanonicalV19WorkerReportAppendTail(
	ctx context.Context,
	tx *sql.Tx,
	homeDir string,
	witness CanonicalV19WorkerReportAppendWitness,
) error {
	checkpoint, found, err := readCanonicalV19WorkerReportCheckpoint(homeDir, witness.AttemptID)
	if err != nil {
		return err
	}
	if witness.Predecessor == nil {
		if found {
			return fmt.Errorf("%w: root witness would replace checkpointed Attempt source history",
				ErrCanonicalV19WorkerReportConflict)
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM worker_report WHERE attempt_id=? LIMIT 1
		)`, witness.AttemptID).Scan(&exists); err != nil {
			return canonicalV19WorkerReportWriteError("read root source boundary", err)
		}
		if exists != 0 {
			return fmt.Errorf("%w: existing Attempt source history requires full replay",
				ErrCanonicalV19WorkerReportWitnessUnproven)
		}
		return nil
	}
	if !found {
		return fmt.Errorf("%w: exact WorkerReport tail checkpoint is missing",
			ErrCanonicalV19WorkerReportWitnessUnproven)
	}
	if checkpoint.WorkerReportID != witness.Predecessor.WorkerReportID ||
		checkpoint.SourcePrefixDigest != witness.Predecessor.SourcePrefixDigest ||
		checkpoint.SourceEndOffset != witness.Predecessor.SourceEndOffset {
		return fmt.Errorf("%w: predecessor WorkerReport %q is not the exact checkpointed source tail",
			ErrCanonicalV19WorkerReportConflict, witness.Predecessor.WorkerReportID)
	}
	return nil
}

func canonicalV19WorkerReportPredecessorMatches(
	predecessor *CanonicalV19WorkerReportPredecessor,
	report CanonicalV19WorkerReport,
) bool {
	return predecessor != nil && predecessor.WorkerReportID == report.ID &&
		predecessor.SourcePrefixDigest == report.SourcePrefixDigest &&
		predecessor.SourceEndOffset == report.SourceEndOffset
}

func requireCanonicalV19WorkerReportCurrent(
	ctx context.Context,
	tx *sql.Tx,
	report CanonicalV19WorkerReport,
) error {
	if report.ExecutorBindingID == "" {
		var attemptID string
		err := tx.QueryRowContext(ctx, `SELECT a.id
			FROM attempt a
			JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
			JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
			JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
			WHERE a.id=? AND a.lifecycle='active' AND a.terminal_at=''`, report.AttemptID).Scan(&attemptID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: Attempt %q lacks exact active ownership",
				ErrCanonicalV19WorkerReportNotCurrent, report.AttemptID)
		}
		if err != nil {
			return canonicalV19WorkerReportWriteError("read exact current Attempt", err)
		}
		return nil
	}

	var attemptID, executorBindingID string
	err := tx.QueryRowContext(ctx, `SELECT a.id,e.id
		FROM executor_binding e
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN plan p ON p.id=a.plan_id AND p.lifecycle='active' AND p.terminal_at=''
		JOIN task t ON t.id=p.task_id AND t.lifecycle='active' AND t.terminal_at=''
		JOIN project project_current ON project_current.id=t.project_id AND project_current.retired_at=''
		JOIN session_binding s ON s.id=e.session_binding_id AND s.attempt_id=a.id AND s.adapter_ref=e.adapter_ref
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=a.id
		WHERE e.id=? AND e.attempt_id=?
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		  AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)`,
		report.ExecutorBindingID, report.AttemptID).Scan(&attemptID, &executorBindingID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Attempt %q / ExecutorBinding %q lacks exact active/open ownership",
			ErrCanonicalV19WorkerReportNotCurrent, report.AttemptID, report.ExecutorBindingID)
	}
	if err != nil {
		return canonicalV19WorkerReportWriteError("read exact current execution ownership", err)
	}
	return nil
}

func loadCanonicalV19WorkerReportByID(
	ctx context.Context,
	tx *sql.Tx,
	id string,
) (CanonicalV19WorkerReport, bool, error) {
	return loadCanonicalV19WorkerReport(ctx, tx, `WHERE id=?`, id)
}

func loadCanonicalV19WorkerReportBySourcePrefix(
	ctx context.Context,
	tx *sql.Tx,
	attemptID string,
	sourcePrefixDigest string,
) (CanonicalV19WorkerReport, bool, error) {
	return loadCanonicalV19WorkerReport(ctx, tx,
		`WHERE attempt_id=? AND source_prefix_digest=?`, attemptID, sourcePrefixDigest)
}

func loadCanonicalV19WorkerReport(
	ctx context.Context,
	tx *sql.Tx,
	where string,
	args ...any,
) (CanonicalV19WorkerReport, bool, error) {
	var report CanonicalV19WorkerReport
	var executorBindingID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,attempt_id,executor_binding_id,source_prefix_digest,
		source_end_offset,report_state,note,created_at FROM worker_report `+where, args...).Scan(
		&report.ID, &report.AttemptID, &executorBindingID, &report.SourcePrefixDigest,
		&report.SourceEndOffset, &report.ReportState, &report.Note, &report.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalV19WorkerReport{}, false, nil
	}
	if err != nil {
		return CanonicalV19WorkerReport{}, false,
			canonicalV19WorkerReportWriteError("read exact WorkerReport identity", err)
	}
	if executorBindingID.Valid {
		report.ExecutorBindingID = executorBindingID.String
	}
	return report, true, nil
}

func loadCanonicalV19WorkerReportsByAttempt(
	ctx context.Context,
	tx *sql.Tx,
	attemptID string,
) ([]CanonicalV19WorkerReport, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,attempt_id,executor_binding_id,source_prefix_digest,
		source_end_offset,report_state,note,created_at FROM worker_report
		WHERE attempt_id=? ORDER BY source_end_offset,id`, attemptID)
	if err != nil {
		return nil, canonicalV19WorkerReportWriteError("read full replay history", err)
	}
	defer func() { _ = rows.Close() }()
	var reports []CanonicalV19WorkerReport
	for rows.Next() {
		var report CanonicalV19WorkerReport
		var executorBindingID sql.NullString
		if err := rows.Scan(&report.ID, &report.AttemptID, &executorBindingID, &report.SourcePrefixDigest,
			&report.SourceEndOffset, &report.ReportState, &report.Note, &report.CreatedAt); err != nil {
			return nil, canonicalV19WorkerReportWriteError("scan full replay history", err)
		}
		if executorBindingID.Valid {
			report.ExecutorBindingID = executorBindingID.String
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, canonicalV19WorkerReportWriteError("iterate full replay history", err)
	}
	return reports, nil
}

func insertCanonicalV19WorkerReport(ctx context.Context, tx *sql.Tx, report CanonicalV19WorkerReport) error {
	var executorBindingID any
	if report.ExecutorBindingID != "" {
		executorBindingID = report.ExecutorBindingID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO worker_report(
		id,attempt_id,executor_binding_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES(?,?,?,?,?,?,?,?)`, report.ID, report.AttemptID, executorBindingID, report.SourcePrefixDigest,
		report.SourceEndOffset, report.ReportState, report.Note, report.CreatedAt); err != nil {
		if isSQLiteConstraint(err) {
			return fmt.Errorf("%w: WorkerReport %q: %v", ErrCanonicalV19WorkerReportConflict, report.ID, err)
		}
		return canonicalV19WorkerReportWriteError("insert exact report", err)
	}
	return nil
}

func canonicalV19WorkerReportWriteError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("ingest canonical v19 WorkerReport: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("ingest canonical v19 WorkerReport: %s: %w", action, err)
}
