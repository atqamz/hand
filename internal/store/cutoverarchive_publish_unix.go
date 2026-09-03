//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func publishLegacyV18CutoverOriginalArchive(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func syncLegacyV18CutoverDirectoryParent(path string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parent.Sync()
}
