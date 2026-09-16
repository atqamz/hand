//go:build !windows

package integration

import "os"

func integrationPathIsIndirect(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func renameIntegrationRoot(root *os.Root, oldPath, newPath string) error {
	return root.Rename(oldPath, newPath)
}
