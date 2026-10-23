package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type BatchFileError struct{ Name, Path string }

func (e *BatchFileError) Error() string {
	return fmt.Sprintf("%s is installed only as %s, a batch file; Hand does not start batch files, because cmd.exe runs them in the wrong folder and mangles the prompt; install the standalone %s, or add the folder of %s.exe to PATH", e.Name, e.Path, e.Name, e.Name)
}

func lookPathExt(name, path, pathext string) (string, error) {
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	var native, batch []string
	for _, ext := range strings.Split(strings.ToLower(pathext), ";") {
		if ext = strings.TrimPrefix(ext, "."); ext == "bat" || ext == "cmd" {
			batch = append(batch, "."+ext)
		} else if ext != "" {
			native = append(native, "."+ext)
		}
	}
	stem, own := name, strings.ToLower(filepath.Ext(name))
	switch {
	case slices.Contains(batch, own):
		stem, batch = strings.TrimSuffix(name, filepath.Ext(name)), append([]string{""}, batch...)
	case slices.Contains(native, own):
		native = append([]string{""}, native...)
	}
	for i, exts := range [][]string{native, batch} {
		for _, dir := range filepath.SplitList(path) {
			if !filepath.IsAbs(dir) {
				continue
			}
			for _, ext := range exts {
				p := filepath.Join(dir, name+ext)
				if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
					continue
				}
				if i == 1 {
					return "", fmt.Errorf("%w: %w", state.ErrInvalid, &BatchFileError{Name: stem, Path: p})
				}
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s not found on PATH", state.ErrInvalid, name)
}
