//go:build !windows

package integration

import "os"

func integrationPathIsIndirect(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
