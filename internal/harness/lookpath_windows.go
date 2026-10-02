package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

func LookPath(name, path string) (string, error) {
	var exts []string
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	for _, ext := range strings.Split(pathext, ";") {
		if ext != "" {
			exts = append(exts, "."+strings.TrimPrefix(ext, "."))
		}
	}
	if slices.ContainsFunc(exts, func(e string) bool { return strings.EqualFold(filepath.Ext(name), e) }) {
		exts = append([]string{""}, exts...)
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(dir, name+ext)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s not found on PATH", state.ErrInvalid, name)
}
