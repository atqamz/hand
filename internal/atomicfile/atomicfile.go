// Package atomicfile replaces a file in one step: content is written to a
// temporary file in the destination directory, then renamed over the target so
// readers never observe a partially written file. This is the single such helper
// for data files (project, watcher, and agentsmd all call it) - do not hand-roll
// another copy. internal/selfupdate stages the replacement binary with the same
// temp-then-rename shape but not through this helper, because it extracts the
// new binary from an archive stream rather than writing bytes it already holds.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Write creates or replaces path with data and mode. tempPrefix names the
// temporary file created alongside path.
func Write(path, tempPrefix string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPrefix)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		removeTemp()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTemp()
		return fmt.Errorf("close temp file: %w", err)
	}
	// Windows denies Rename while a reader or scanner holds the new file
	// open (concurrent reads, first-write AV sweeps); the swap retries on an
	// exponential curve to a ~2s ceiling. Unix never hits this path.
	var renameErr error
	for attempt := range 8 {
		renameErr = os.Rename(tmpName, path)
		if renameErr == nil {
			return nil
		}
		if !isTransientRenameDenied(renameErr) || attempt == 7 {
			break
		}
		time.Sleep((time.Duration(1) << uint(attempt)) * 15 * time.Millisecond)
	}
	if renameErr != nil {
		removeTemp()
		return fmt.Errorf("rename temp file: %w", renameErr)
	}
	return nil
}

func isTransientRenameDenied(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// 32 = ERROR_ACCESS_DENIED, 33 = ERROR_SHARING_VIOLATION; the Win32
	// constants live in x/sys, which this tiny helper must not pull in.
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == 32 || errno == 33
	}
	return false
}
