//go:build windows

package store

import (
	"golang.org/x/sys/windows"
)

const legacyV18CutoverMoveFileWriteThrough = 0x00000008

func publishLegacyV18CutoverOriginalArchive(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, legacyV18CutoverMoveFileWriteThrough)
}

func syncLegacyV18CutoverDirectoryParent(string) error {
	// Windows does not expose a POSIX-style directory fsync. The authoritative
	// publication itself uses MoveFileEx with MOVEFILE_WRITE_THROUGH, which does
	// not return until the move has reached disk.
	return nil
}
