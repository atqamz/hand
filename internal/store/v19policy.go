package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// CanonicalV19PolicyRecordInput captures declared policy references, not resolved
// configuration or capability evidence. Empty references confer no exemption.
type CanonicalV19PolicyRecordInput struct {
	ID, ProjectID, SupersedesID string
	WorkerProfileRef            string
	QualificationPolicyRef      string
	IntegrationPolicyRef        string
	ProductionPolicyRef         string
	PublicationPolicyRef        string
}

// RecordCanonicalV19Policy records an initial policy or replaces exactly the
// named current revision. Historical Plans keep their original policy identity.
// Duplicate IDs refuse, including retries after a lost response.
func RecordCanonicalV19Policy(ctx context.Context, homeDir string, input CanonicalV19PolicyRecordInput) (int64, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.ID == "" || input.ProjectID == "" {
		return 0, "", fmt.Errorf("record canonical policy: PolicyRevision and Project IDs are required")
	}
	refs := []string{input.WorkerProfileRef, input.QualificationPolicyRef, input.IntegrationPolicyRef,
		input.ProductionPolicyRef, input.PublicationPolicyRef}
	for _, ref := range refs {
		if len(ref) > 512 || !utf8.ValidString(ref) || strings.IndexFunc(ref, func(r rune) bool {
			return unicode.IsControl(r) || unicode.IsSpace(r)
		}) >= 0 {
			return 0, "", fmt.Errorf("record canonical policy: references must be bounded non-secret identifiers without whitespace")
		}
	}
	// The ordered tuple covers exactly the five typed references in policy_revision.
	// It is not a copy of mutable Route/Profile definitions or secret material.
	payload, err := json.Marshal(refs)
	if err != nil {
		return 0, "", err
	}
	digest := canonicalV19SHA256(append([]byte("hand:v19:policy-references:v1\x00"), payload...))
	db, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return 0, "", err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM project WHERE id=? AND retired_at=''`, input.ProjectID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", ErrCanonicalV19ProjectNotCurrent
		}
		return 0, "", err
	}
	var currentID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM policy_revision WHERE project_id=? AND superseded_at=''`, input.ProjectID).Scan(&currentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	if currentID != input.SupersedesID {
		return 0, "", fmt.Errorf("record canonical policy: expected predecessor is not the exact current PolicyRevision")
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM policy_revision WHERE project_id=?`, input.ProjectID).Scan(&ordinal); err != nil {
		return 0, "", err
	}
	// Missing current evidence after prior history is not a fresh initial policy.
	if currentID == "" && ordinal != 1 {
		return 0, "", fmt.Errorf("record canonical policy: prior history has no current revision")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if currentID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE policy_revision SET superseded_at=? WHERE id=? AND project_id=? AND superseded_at=''`, now, currentID, input.ProjectID); err != nil {
			return 0, "", err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,
		worker_profile_ref,qualification_policy_ref,integration_policy_ref,production_policy_ref,publication_policy_ref,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, input.ID, input.ProjectID, ordinal, digest,
		input.WorkerProfileRef, input.QualificationPolicyRef, input.IntegrationPolicyRef,
		input.ProductionPolicyRef, input.PublicationPolicyRef, now)
	if err != nil {
		return 0, "", fmt.Errorf("record canonical policy: insert revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return ordinal, digest, nil
}
