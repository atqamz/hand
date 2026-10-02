package luvus

import (
	"os"
	"path/filepath"
	"strings"
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
