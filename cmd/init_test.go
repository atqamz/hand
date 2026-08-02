package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/secondhand/internal/home"
)

func TestInitCreatesTheHandDbMarker(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "state", "hand.db")); err != nil {
		t.Fatalf("state/hand.db missing after init: %v", err)
	}
	ok, err := home.IsHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("got IsHome false right after init, want true")
	}
}

func TestInitIsIdempotentAboutTheHandDbMarker(t *testing.T) {
	t.Setenv("HAND_HOME", "")
	dir := t.TempDir()
	t.Chdir(dir)

	for i := 0; i < 2; i++ {
		cmd := newInitCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "state", "hand.db")); err != nil {
		t.Fatalf("state/hand.db missing after repeat init: %v", err)
	}
}
