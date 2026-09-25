//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
)

func moveLegacyV18CutoverNoReplaceDurable(source, target string) error {
	if err := linkLegacyV18CutoverNoReplaceDurable(source, target); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return syncLegacyV18CutoverDirectory(filepath.Dir(source))
}

func linkLegacyV18CutoverNoReplaceDurable(source, target string) error {
	if err := os.Link(source, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// A crash may have happened after the target hardlink was created. Resume only
		// when both names still identify the exact same inode; never a byte-equal file.
		sourceInfo, sourceErr := os.Lstat(source)
		if sourceErr != nil {
			return err
		}
		targetInfo, targetErr := os.Lstat(target)
		if targetErr != nil || !os.SameFile(sourceInfo, targetInfo) {
			return err
		}
	}
	// Repeat the directory sync on resume: the earlier attempt may have created the
	// link but failed before making that directory entry durable.
	return syncLegacyV18CutoverDirectory(filepath.Dir(target))
}

func replaceLegacyV18CutoverDurable(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return syncLegacyV18CutoverDirectoryParent(target)
}
