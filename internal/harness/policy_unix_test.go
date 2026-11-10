//go:build unix

package harness

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStarterPolicyWriteFailureLeavesNoFile(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStarterPolicyUnderASmallFileLimit$")
	cmd.Env = append(os.Environ(), "HAND_TEST_FSIZE_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starter write under an 2-byte file size limit: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, PolicyFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial policy left behind: %v", err)
	}
}

func TestStarterPolicyUnderASmallFileLimit(t *testing.T) {
	home := os.Getenv("HAND_TEST_FSIZE_HOME")
	if home == "" {
		t.Skip("runs only as the child of TestStarterPolicyWriteFailureLeavesNoFile")
	}
	small := syscall.Rlimit{Cur: 2, Max: 2}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &small); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteStarterPolicy(home); err == nil {
		t.Fatal("starter write under an 2-byte file size limit succeeded")
	}
}
