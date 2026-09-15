package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/atomicfile"
)

const canonicalV19WorkerReportCheckpointVersion = 1

type canonicalV19WorkerReportCheckpoint struct {
	Version            int    `json:"version"`
	AttemptID          string `json:"attempt_id"`
	WorkerReportID     string `json:"worker_report_id,omitempty"`
	SourcePrefixDigest string `json:"source_prefix_digest,omitempty"`
	SourceEndOffset    int64  `json:"source_end_offset,omitempty"`
	Uncertain          bool   `json:"uncertain,omitempty"`
}

func canonicalV19WorkerReportCheckpointPath(homeDir, attemptID string) string {
	digest := sha256.Sum256([]byte(attemptID))
	return filepath.Join(Dir(homeDir), ".worker-report-"+hex.EncodeToString(digest[:])+".checkpoint.json")
}

func lockCanonicalV19WorkerReportCheckpoint(homeDir, attemptID string) (func(), error) {
	unlock, err := Lock(homeDir, "canonical-v19-worker-report-checkpoint:"+attemptID, false)
	if err != nil {
		return nil, fmt.Errorf("lock canonical v19 WorkerReport checkpoint: %w", err)
	}
	return unlock, nil
}

func readCanonicalV19WorkerReportCheckpoint(
	homeDir string,
	attemptID string,
) (canonicalV19WorkerReportCheckpoint, bool, error) {
	data, err := os.ReadFile(canonicalV19WorkerReportCheckpointPath(homeDir, attemptID))
	if errors.Is(err, os.ErrNotExist) {
		return canonicalV19WorkerReportCheckpoint{}, false, nil
	}
	if err != nil {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("read canonical v19 WorkerReport checkpoint: %w", err)
	}
	var checkpoint canonicalV19WorkerReportCheckpoint
	if json.Unmarshal(data, &checkpoint) != nil || checkpoint.Version != canonicalV19WorkerReportCheckpointVersion ||
		checkpoint.AttemptID != attemptID || checkpoint.Uncertain || checkpoint.WorkerReportID == "" ||
		checkpoint.SourcePrefixDigest == "" || checkpoint.SourceEndOffset <= 0 {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("%w: WorkerReport checkpoint is missing, uncertain, or corrupt",
				ErrCanonicalV19WorkerReportWitnessUnproven)
	}
	return checkpoint, true, nil
}

func writeCanonicalV19WorkerReportCheckpoint(
	homeDir string,
	checkpoint canonicalV19WorkerReportCheckpoint,
) error {
	checkpoint.Version = canonicalV19WorkerReportCheckpointVersion
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode canonical v19 WorkerReport checkpoint: %w", err)
	}
	if err := atomicfile.Write(canonicalV19WorkerReportCheckpointPath(homeDir, checkpoint.AttemptID),
		".worker-report-checkpoint-", append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write canonical v19 WorkerReport checkpoint: %w", err)
	}
	return nil
}

func markCanonicalV19WorkerReportCheckpointUncertain(homeDir, attemptID string) error {
	return writeCanonicalV19WorkerReportCheckpoint(homeDir, canonicalV19WorkerReportCheckpoint{
		AttemptID: attemptID,
		Uncertain: true,
	})
}

func writeCanonicalV19WorkerReportCheckpointTail(homeDir string, report CanonicalV19WorkerReport) error {
	return writeCanonicalV19WorkerReportCheckpoint(homeDir, canonicalV19WorkerReportCheckpoint{
		AttemptID: report.AttemptID, WorkerReportID: report.ID,
		SourcePrefixDigest: report.SourcePrefixDigest, SourceEndOffset: report.SourceEndOffset,
	})
}

func removeCanonicalV19WorkerReportCheckpoint(homeDir, attemptID string) error {
	err := os.Remove(canonicalV19WorkerReportCheckpointPath(homeDir, attemptID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove canonical v19 WorkerReport checkpoint: %w", err)
	}
	return nil
}
