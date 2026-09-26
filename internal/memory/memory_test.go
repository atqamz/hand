package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitCreatesLayoutAndKeepsExistingOperatorMemory(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	op := filepath.Join(home, "memory", OperatorFile)
	if err := os.WriteFile(op, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(op)
	if err != nil || string(got) != "mine\n" {
		t.Fatalf("operator memory = %q, %v; want untouched", got, err)
	}
	if fi, err := os.Stat(filepath.Join(home, "memory", "projects")); err != nil || !fi.IsDir() {
		t.Fatalf("projects dir: %v", err)
	}
}
