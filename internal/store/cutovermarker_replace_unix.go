//go:build !windows

package store

import "os"

func replaceLegacyV18CutoverMarker(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return syncLegacyV18CutoverDirectoryParent(target)
}
