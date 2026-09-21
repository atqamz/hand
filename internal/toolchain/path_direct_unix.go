//go:build !windows

package toolchain

import "os"

func runtimePathIsIndirect(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func renameRuntimeRoot(root *os.Root, oldPath, newPath string) error {
	return root.Rename(oldPath, newPath)
}
