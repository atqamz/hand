//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

var errLegacyV18CutoverReplaceBusy = errors.New("another process holds state/hand.db open; stop it and retry")

func moveLegacyV18CutoverNoReplaceDurable(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	// No MOVEFILE_REPLACE_EXISTING: an unexpected destination is a hard stop.
	// MOVEFILE_WRITE_THROUGH keeps the same-volume rename durable before return.
	return windows.MoveFileEx(from, to, legacyV18CutoverMoveFileWriteThrough)
}

func linkLegacyV18CutoverNoReplaceDurable(source, target string) error {
	if err := os.Link(source, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		sourceInfo, sourceErr := os.Lstat(source)
		if sourceErr != nil {
			return err
		}
		targetInfo, targetErr := os.Lstat(target)
		if targetErr != nil || !os.SameFile(sourceInfo, targetInfo) {
			return err
		}
	}
	return nil
}

// A handle another process holds on the target stops the replacement; the caller retries later.
func replaceLegacyV18CutoverDurable(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, legacyV18CutoverMoveFileReplaceExisting|legacyV18CutoverMoveFileWriteThrough)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w: %v", errLegacyV18CutoverReplaceBusy, err)
	}
	return err
}
