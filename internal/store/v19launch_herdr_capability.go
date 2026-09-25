package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/launch"
)

// ErrCanonicalV19HerdrCapabilityUnsupported marks a canonical operation that
// the selected managed Herdr provider cannot prove safely.
var ErrCanonicalV19HerdrCapabilityUnsupported = errors.New("canonical v19 Herdr capability is unsupported")

const canonicalV19ExecGuardKeyFamily = "herdr-exec-guard:"

func canonicalV19HerdrCapabilityUnsupported(operation, proof string) error {
	return fmt.Errorf("%w: %s requires provider-backed %s", ErrCanonicalV19HerdrCapabilityUnsupported, operation, proof)
}

func canonicalV19HerdrUnsupportedEvidenceDigest(operation, operationID, requestDigest, state string) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-unsupported-transition:v1")
	writeCanonicalV19DigestField(hash, "operation", operation)
	writeCanonicalV19DigestField(hash, "operation_id", operationID)
	writeCanonicalV19DigestField(hash, "request_digest", requestDigest)
	writeCanonicalV19DigestField(hash, "state", state)
	return hex.EncodeToString(hash.Sum(nil))
}

// Only provider proof that the caller belongs to the exact ExecutorBinding may
// create this token. No selected Herdr provider can create one yet.
type canonicalV19WorkerInputCallerAttestation struct {
	executorBindingID string
}

// This runs inside the drain/acknowledge transaction so the exec-guard credential check
// and the currentness it depends on (Fleet, binding, termination, Attempt) observe one snapshot.
func requireCanonicalV19WorkerInputCallerAttestation(
	ctx context.Context,
	tx *sql.Tx,
	executorBindingID string,
	attestation *canonicalV19WorkerInputCallerAttestation,
	credential string,
) error {
	if attestation != nil && attestation.executorBindingID == executorBindingID {
		return nil
	}
	return verifyCanonicalV19WorkerInputCredential(ctx, tx, executorBindingID, credential)
}

func verifyCanonicalV19WorkerInputCredential(ctx context.Context, tx *sql.Tx, executorBindingID, credential string) error {
	unsupported := canonicalV19HerdrCapabilityUnsupported("WorkerInput protocol", "exact caller-to-ExecutorBinding attestation")
	secret, err := base64.RawURLEncoding.DecodeString(credential)
	if credential == "" || err != nil || len(secret) != 32 {
		return unsupported
	}
	var fleetID string
	if err := tx.QueryRowContext(ctx, `SELECT fleet_id FROM fleet WHERE singleton=1`).Scan(&fleetID); err != nil {
		return canonicalV19WorkerInputCallerAttestationReadError("read Fleet identity", err)
	}
	var providerExecutorKey, stored string
	err = tx.QueryRowContext(ctx, `SELECT e.provider_executor_key, le.value_digest
		FROM executor_binding e
		JOIN attempt a ON a.id=e.attempt_id AND a.lifecycle='active' AND a.terminal_at=''
		JOIN launch_environment le ON le.operation_id=e.launch_operation_id AND le.name=?
		WHERE e.id=?
		  AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)`,
		execguard.CredentialEnv, executorBindingID).Scan(&providerExecutorKey, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return unsupported
	}
	if err != nil {
		return canonicalV19WorkerInputCallerAttestationReadError("read exact ExecutorBinding credential", err)
	}
	if !strings.HasPrefix(providerExecutorKey, canonicalV19ExecGuardKeyPrefix) {
		return unsupported
	}
	verifier := launch.ExecGuardCredentialVerifier(fleetID, executorBindingID, credential)
	if subtle.ConstantTimeCompare([]byte(verifier), []byte(stored)) != 1 {
		return unsupported
	}
	return nil
}

func canonicalV19WorkerInputCallerAttestationReadError(action string, err error) error {
	if isSQLiteBusy(err) {
		return fmt.Errorf("verify canonical v19 WorkerInput caller attestation: %s: %w", action, ErrContention)
	}
	return fmt.Errorf("verify canonical v19 WorkerInput caller attestation: %s: %w", action, err)
}
