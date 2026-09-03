//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func publishLegacyV18CutoverManifest(source, target string) error {
	// Link publishes the final manifest name atomically without replacing any
	// existing evidence. The candidate remains non-authoritative until the final
	// directory entry is durable and the candidate name is removed.
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
