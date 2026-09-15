//go:build windows

package integration

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func integrationPathIsIndirect(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func renameIntegrationRoot(root *os.Root, oldPath, newPath string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = root.Rename(oldPath, newPath)
		if err == nil || !integrationRenameRetryable(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func integrationRenameRetryable(err error) bool {
	var code syscall.Errno
	return errors.As(err, &code) && (code == syscall.ERROR_ACCESS_DENIED || code == syscall.Errno(32))
}
