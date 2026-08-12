//go:build !windows

package selfupdate

import "os"

func replaceExecutable(execPath, stagedPath string) error {
	return os.Rename(stagedPath, execPath)
}
