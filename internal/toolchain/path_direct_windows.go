//go:build windows

package toolchain

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func runtimePathIsIndirect(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// Windows can briefly deny a rooted rename while another handle is closing.
// Keep the bounded retry that makes atomic publication reliable without
// turning a persistent ownership error into an unbounded wait.
func renameRuntimeRoot(root *os.Root, oldPath, newPath string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = root.Rename(oldPath, newPath)
		if err == nil || !runtimeRenameRetryable(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func runtimeRenameRetryable(err error) bool {
	var code syscall.Errno
	return errors.As(err, &code) && (code == syscall.ERROR_ACCESS_DENIED || code == syscall.Errno(32))
}
