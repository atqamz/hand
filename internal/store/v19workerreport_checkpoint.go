package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const canonicalV19WorkerReportCheckpointVersion = 1

const canonicalV19WorkerReportCheckpointMaxBytes = 512

type canonicalV19WorkerReportCheckpointFaultStage string

const (
	canonicalV19WorkerReportCheckpointAfterUncertain canonicalV19WorkerReportCheckpointFaultStage = "after-uncertain"
	canonicalV19WorkerReportCheckpointAfterCommit    canonicalV19WorkerReportCheckpointFaultStage = "after-commit"
)

type canonicalV19WorkerReportCheckpointFaultKey struct{}

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
	file, err := os.Open(canonicalV19WorkerReportCheckpointPath(homeDir, attemptID))
	if errors.Is(err, os.ErrNotExist) {
		return canonicalV19WorkerReportCheckpoint{}, false, nil
	}
	if err != nil {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("read canonical v19 WorkerReport checkpoint: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, canonicalV19WorkerReportCheckpointMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("read canonical v19 WorkerReport checkpoint: %w", readErr)
	}
	if closeErr != nil {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("close canonical v19 WorkerReport checkpoint: %w", closeErr)
	}
	if len(data) > canonicalV19WorkerReportCheckpointMaxBytes {
		return canonicalV19WorkerReportCheckpoint{}, false,
			fmt.Errorf("%w: WorkerReport checkpoint exceeds %d bytes",
				ErrCanonicalV19WorkerReportWitnessUnproven, canonicalV19WorkerReportCheckpointMaxBytes)
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
	if err := writeCanonicalV19WorkerReportCheckpointFile(
		canonicalV19WorkerReportCheckpointPath(homeDir, checkpoint.AttemptID),
		".worker-report-checkpoint-", append(encoded, '\n'), 0o600,
	); err != nil {
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

// Checkpoint publication needs stronger ordering than a visibility-only atomic
// replacement: content is synced before replace, then the new directory entry
// is synced (or Windows uses write-through replacement).
func writeCanonicalV19WorkerReportCheckpointFile(
	path string,
	tempPrefix string,
	data []byte,
	mode os.FileMode,
) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := publishCanonicalV19WorkerReportCheckpoint(tmpName, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func injectCanonicalV19WorkerReportCheckpointFault(
	ctx context.Context,
	stage canonicalV19WorkerReportCheckpointFaultStage,
) error {
	fault, _ := ctx.Value(canonicalV19WorkerReportCheckpointFaultKey{}).(func(canonicalV19WorkerReportCheckpointFaultStage) error)
	if fault == nil {
		return nil
	}
	return fault(stage)
}
