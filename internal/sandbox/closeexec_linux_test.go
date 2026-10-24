//go:build linux

package sandbox

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCloseOnExecMarksExtraFiles(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	CloseOnExec()
	for _, f := range []*os.File{r, w} {
		flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
		if err != nil {
			t.Fatal(err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("fd %d is not close-on-exec", f.Fd())
		}
	}
}
