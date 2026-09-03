//go:build windows

package store

import "golang.org/x/sys/windows"

func publishLegacyV18CutoverManifest(source, target string) error {
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
