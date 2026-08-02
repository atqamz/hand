package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/agentsmd"
)

func TestDoctorCleanFleetHomeExitsZeroSilently(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil for a clean AGENTS.md", err)
	}
	if out.String() != "" {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
}

func TestDoctorReportsViolationsAndExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "AGENTS.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nFixed on 2026-07-29.\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	err = cmd.Execute()
	if err == nil {
		t.Fatal("got nil error, want a non-nil error for a perishable-content hit")
	}
	if !strings.Contains(out.String(), "AGENTS.md:") {
		t.Fatalf("stdout = %q, want a per-line AGENTS.md violation", out.String())
	}
}

func TestDoctorOutsideFleetHomeIsPrecondition(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HAND_HOME", "")

	cmd := newDoctorCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("got nil error, want a precondition failure outside a fleet home")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want an ExitError with code 3", err)
	}
}
