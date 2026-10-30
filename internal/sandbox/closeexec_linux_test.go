//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCloseOnExecMarksDescriptorsAboveTwo(t *testing.T) {
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if err := CloseOnExec(); err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("descriptor above 2 kept across exec")
	}
}

func TestCloseOnExecReportsAnUnreadableProc(t *testing.T) {
	old := procSelfFD
	defer func() { procSelfFD = old }()
	procSelfFD = filepath.Join(t.TempDir(), "missing")
	if err := CloseOnExec(); err == nil {
		t.Fatal("a missing proc fd folder was accepted")
	}
}
