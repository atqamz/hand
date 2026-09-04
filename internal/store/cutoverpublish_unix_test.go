//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveLegacyV18CutoverNoReplaceDurableResumesSameInodeHardlink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	target := filepath.Join(dir, "target.db")
	payload := []byte("exact cutover bytes")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncLegacyV18CutoverFile(source); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after link(2) created the destination but before source
	// removal completed. The retry is allowed only because both names are the
	// same inode, not merely because their bytes happen to match.
	if err := os.Link(source, target); err != nil {
		t.Fatal(err)
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("source after crash-resume move: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("target payload = %q, want %q", got, payload)
	}
}

func TestMoveLegacyV18CutoverNoReplaceDurableRefusesDifferentExistingInode(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(source, []byte("same bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("same bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveLegacyV18CutoverNoReplaceDurable(source, target); err == nil {
		t.Fatal("different existing inode was accepted")
	}
	if _, err := os.Lstat(source); err != nil {
		t.Fatalf("source changed after refusal: %v", err)
	}
}
