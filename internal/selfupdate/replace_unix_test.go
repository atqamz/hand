//go:build !windows

package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceExecutableRenamesStagedFile(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "hand")
	stagedPath := filepath.Join(dir, ".hand-update-staged")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutable(execPath, stagedPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q, want new", got)
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged path error = %v, want not exist", err)
	}
}
