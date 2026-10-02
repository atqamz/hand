//go:build unix

package harness

import (
	"errors"
	"github.com/atqamz/hand/internal/state"
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathWantsAnAbsoluteExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LookPath("codex", "relative:"+dir); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("non-executable err = %v", err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath("codex", "relative:"+dir); err != nil || got != bin {
		t.Fatalf("lookpath = %s, %v", got, err)
	}
}
