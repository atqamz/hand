//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
)

func moveLegacyV18CutoverNoReplaceDurable(source, target string) error {
	if err := os.Link(source, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// A crash may have happened after the durable target link was created but
		// before the source name was removed. Resume only when both names still
		// identify the exact same inode; never accept a merely byte-equal file.
		sourceInfo, sourceErr := os.Lstat(source)
		if sourceErr != nil {
			return err
		}
		targetInfo, targetErr := os.Lstat(target)
		if targetErr != nil || !os.SameFile(sourceInfo, targetInfo) {
			return err
		}
	} else if err := syncLegacyV18CutoverDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return syncLegacyV18CutoverDirectory(filepath.Dir(source))
}
