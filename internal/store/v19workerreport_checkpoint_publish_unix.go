//go:build !windows

package store

import (
	"fmt"
	"os"
	"path/filepath"
)

func publishCanonicalV19WorkerReportCheckpoint(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return fmt.Errorf("open checkpoint directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync checkpoint directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close checkpoint directory: %w", err)
	}
	return nil
}
