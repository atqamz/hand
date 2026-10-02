//go:build unix

package harness

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStarterPolicyWriteFailureLeavesNoFile(t *testing.T) {
	home := t.TempDir()
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old); err != nil {
		t.Fatal(err)
	}
	small := old
	small.Cur = 8
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &small); err != nil {
		t.Fatal(err)
	}
	_, err := WriteStarterPolicy(home)
	if rerr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old); rerr != nil {
		t.Fatal(rerr)
	}
	if err == nil {
		t.Fatal("starter write under an 8-byte file size limit succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, PolicyFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial policy left behind: %v", err)
	}
}
