//go:build windows

package store

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func publishCanonicalV19WorkerReportCheckpoint(source, target string) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	for attempt := range 8 {
		err = windows.MoveFileEx(sourcePtr, targetPtr, flags)
		if err == nil {
			return nil
		}
		if !canonicalV19WorkerReportCheckpointReplaceDenied(err) || attempt == 7 {
			break
		}
		time.Sleep((time.Duration(1) << uint(attempt)) * 15 * time.Millisecond)
	}
	return fmt.Errorf("replace temp file with write-through: %w", err)
}

func canonicalV19WorkerReportCheckpointReplaceDenied(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_SHARING_VIOLATION)
}
