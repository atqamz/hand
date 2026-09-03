//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func publishLegacyV18CutoverOriginalArchive(source, target string) error {
	// Link publishes the target name atomically without the overwrite semantics
	// of os.Rename. Source and target are the same inode until the non-authoritative
	// candidate name is removed after the target directory entry is durable.
	if err := os.Link(source, target); err != nil {
		return err
	}
	if err := syncLegacyV18CutoverDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return syncLegacyV18CutoverDirectory(filepath.Dir(source))
}

func syncLegacyV18CutoverDirectoryParent(path string) error {
	return syncLegacyV18CutoverDirectory(filepath.Dir(path))
}

func syncLegacyV18CutoverDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
