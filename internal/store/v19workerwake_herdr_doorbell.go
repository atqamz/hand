package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

const canonicalV19HerdrWorkerWakeDoorbellMaxBytes = 1024

// CanonicalV19HerdrWorkerWakeDoorbellInput identifies the immutable values encoded
// into the bounded Hand-owned Herdr wake prompt.
type CanonicalV19HerdrWorkerWakeDoorbellInput struct {
	OperationID           string
	AttemptID             string
	ExecutorBindingID     string
	PendingThroughOrdinal int64
}

// CanonicalV19HerdrWorkerWakeDoorbellDigest returns the digest a Herdr WorkerWake
// request must persist for the exact bounded doorbell bytes this adapter will submit.
func CanonicalV19HerdrWorkerWakeDoorbellDigest(input CanonicalV19HerdrWorkerWakeDoorbellInput) (string, error) {
	doorbell, err := canonicalV19HerdrWorkerWakeDoorbellFor(input)
	if err != nil {
		return "", err
	}
	return doorbell.Digest, nil
}

type canonicalV19HerdrWorkerWakeDoorbell struct {
	Text   string
	Digest string
}

func canonicalV19HerdrWorkerWakeDoorbellFor(input CanonicalV19HerdrWorkerWakeDoorbellInput) (canonicalV19HerdrWorkerWakeDoorbell, error) {
	if input.OperationID == "" || input.AttemptID == "" || input.ExecutorBindingID == "" {
		return canonicalV19HerdrWorkerWakeDoorbell{}, fmt.Errorf("build canonical v19 Herdr WorkerWake doorbell: operation, Attempt, and ExecutorBinding IDs are required")
	}
	if input.PendingThroughOrdinal <= 0 {
		return canonicalV19HerdrWorkerWakeDoorbell{}, fmt.Errorf("build canonical v19 Herdr WorkerWake doorbell: pending-through ordinal must be positive")
	}

	markerHash := sha256.New()
	writeCanonicalV19DigestField(markerHash, "domain", "hand:v19:herdr-worker-wake-marker:v1")
	writeCanonicalV19DigestField(markerHash, "operation_id", input.OperationID)
	writeCanonicalV19DigestField(markerHash, "attempt_id", input.AttemptID)
	writeCanonicalV19DigestField(markerHash, "executor_binding_id", input.ExecutorBindingID)
	writeCanonicalV19DigestField(markerHash, "pending_through_ordinal", strconv.FormatInt(input.PendingThroughOrdinal, 10))
	marker := hex.EncodeToString(markerHash.Sum(nil))[:16]

	drainArgv, err := json.Marshal([]string{
		"runtime", "worker-input", "drain", input.AttemptID, input.ExecutorBindingID,
	})
	if err != nil {
		return canonicalV19HerdrWorkerWakeDoorbell{}, fmt.Errorf("build canonical v19 Herdr WorkerWake doorbell drain argv: %w", err)
	}
	ackArgv, err := json.Marshal([]string{
		"runtime", "worker-input", "acknowledge", "<worker-input-id>", input.ExecutorBindingID,
	})
	if err != nil {
		return canonicalV19HerdrWorkerWakeDoorbell{}, fmt.Errorf("build canonical v19 Herdr WorkerWake doorbell acknowledgement argv: %w", err)
	}

	text := fmt.Sprintf(
		"HAND_WORKER_WAKE_V1 marker=%s pending_through=%d. Canonical WorkerInput is pending. Invoke `hand` with argv %s. Acknowledge only each input actually observed by invoking `hand` with argv %s after replacing <worker-input-id>.",
		marker, input.PendingThroughOrdinal, drainArgv, ackArgv,
	)
	if len(text) > canonicalV19HerdrWorkerWakeDoorbellMaxBytes {
		return canonicalV19HerdrWorkerWakeDoorbell{}, fmt.Errorf("build canonical v19 Herdr WorkerWake doorbell: %d bytes exceeds %d-byte bound", len(text), canonicalV19HerdrWorkerWakeDoorbellMaxBytes)
	}
	digest := sha256.Sum256([]byte(text))
	return canonicalV19HerdrWorkerWakeDoorbell{Text: text, Digest: hex.EncodeToString(digest[:])}, nil
}
