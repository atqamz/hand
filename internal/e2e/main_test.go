package e2e_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var handBin string

func enabled(t testing.TB) {
	t.Helper()
	if os.Getenv("HAND_E2E") != "1" {
		t.Skip("set HAND_E2E=1 to run against the real harness binaries on PATH")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("HAND_E2E") != "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "hand-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	handBin = filepath.Join(dir, "hand")
	if runtime.GOOS == "windows" {
		handBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", handBin, "github.com/atqamz/hand")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
