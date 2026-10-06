package luvus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func executable(fi os.FileInfo) bool {
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	for _, ext := range strings.Split(pathext, ";") {
		if ext != "" && strings.EqualFold(filepath.Ext(fi.Name()), "."+strings.TrimPrefix(ext, ".")) {
			return fi.Mode().IsRegular()
		}
	}
	return false
}

func exeExt(bin string) string { return strings.ToLower(filepath.Ext(bin)) }

func held(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

func syncDir(string) error { return nil }
