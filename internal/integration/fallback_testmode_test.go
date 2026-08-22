//go:build test

package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func TestTestModeRunPrefersPATHFakeOverPrivateCapability(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SECONDHAND_HOME", root)

	privateBin := t.TempDir()
	faketool.Command{Name: "gh", Stdout: "private\n"}.Install(t, privateBin)
	privateSource := filepath.Join(privateBin, "gh")
	if runtime.GOOS == "windows" {
		privateSource += ".exe"
	}
	store := NewStore(root)
	installed, err := store.Install("github/gh", privateSource)
	if err != nil {
		t.Fatal(err)
	}
	privateConfig, err := os.ReadFile(filepath.Join(privateBin, ".gh-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(installed), ".gh-config.json"), privateConfig, 0o644); err != nil {
		t.Fatal(err)
	}

	pathBin := faketool.Bin(t)
	faketool.Command{Name: "gh", Stdout: "path\n"}.Install(t, pathBin)
	stdout, stderr, err := Run(context.Background(), "github/gh", "")
	if err != nil {
		t.Fatalf("Run error = %v, stderr = %q", err, stderr)
	}
	if string(stdout) != "path\n" {
		t.Fatalf("Run stdout = %q, want PATH fake output", stdout)
	}
}
