package memory

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
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

func TestReadIsBoundedAndAlwaysValidUTF8(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	op := filepath.Join(home, "memory", OperatorFile)
	if err := os.WriteFile(op, append([]byte("héllo "), 0xff, 0xfe, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	text, truncated, err := Read(home, OperatorFile, 2)
	if err != nil || !truncated || text != "h" {
		t.Fatalf("Read = %q, %v, %v; want %q truncated mid-rune", text, truncated, err, "h")
	}
	full, truncated, err := Read(home, OperatorFile, 1<<20)
	if err != nil || truncated || !utf8.ValidString(full) {
		t.Fatalf("full Read = %q, %v, %v", full, truncated, err)
	}
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	text, truncated, err := Read(t.TempDir(), OperatorFile, 100)
	if err != nil || truncated || text != "" {
		t.Fatalf("Read = %q, %v, %v", text, truncated, err)
	}
}
