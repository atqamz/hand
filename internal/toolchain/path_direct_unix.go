//go:build !windows

package toolchain

import "os"

func runtimePathIsIndirect(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
