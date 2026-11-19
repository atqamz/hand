//go:build darwin

package launcher

import (
	"os"
	"path/filepath"
)

const Marker = "no HandManaged key in Info.plist"

var handPath = func() string {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return ""
	}
	return exe
}

func Sync(getenv func(string) string, bin string) (string, error) {
	return bundleSync(getenv("HOME"), bin, handPath())
}

func Owned(getenv func(string) string) ([]string, string, error) {
	return bundleOwned(getenv("HOME"))
}

func Remove(getenv func(string) string) (string, error) { return bundleRemove(getenv("HOME")) }

func Check(getenv func(string) string, bin string) (State, string) {
	return bundleCheck(getenv("HOME"), bin, handPath())
}
