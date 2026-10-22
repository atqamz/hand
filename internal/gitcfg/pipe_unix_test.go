//go:build unix

package gitcfg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitReturnsWhenAChildHoldsThePipe(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 30 &\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	began := time.Now()
	if _, err := run(context.Background(), t.TempDir(), "status"); time.Since(began) > 5*time.Second {
		t.Fatalf("git = %v after %s", err, time.Since(began))
	}
}
