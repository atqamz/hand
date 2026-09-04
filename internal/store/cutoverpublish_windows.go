//go:build windows

package store

import "golang.org/x/sys/windows"

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
