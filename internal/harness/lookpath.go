package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type BatchFileError struct{ Name, Path string }

func (e *BatchFileError) Error() string {
	msg := fmt.Sprintf("%s is installed only as %s, a batch file; Hand does not start batch files, because cmd.exe runs them in the wrong folder and mangles the prompt; install the standalone %s, or add the folder of %s.exe to PATH", e.Name, e.Path, e.Name, e.Name)
	if _, ok := npmNative[e.Name]; ok {
		msg += "; an npm install leaves only the shim on PATH"
	}
	return msg
}

const minNativeSize = 1 << 20

var npmNative = map[string]string{
	"claude":   "node_modules/@anthropic-ai/claude-code/bin/claude.exe",
	"codex":    "node_modules/@openai/codex/node_modules/@openai/codex-win32-*/vendor/*/bin/codex.exe",
	"copilot":  "node_modules/@github/copilot/node_modules/@github/copilot-win32-*/copilot.exe",
	"opencode": "node_modules/opencode-ai/bin/opencode.exe",
}

func npmExe(stem, shim, arch string) (string, bool) {
	pattern, ok := npmNative[stem]
	if !ok {
		return "", false
	}
	dir := filepath.Dir(shim)
	matches, _ := fs.Glob(os.DirFS(dir), pattern)
	var emulated string
	for _, m := range matches {
		p := filepath.Join(dir, filepath.FromSlash(m))
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() || fi.Size() < minNativeSize {
			continue
		}
		switch arm := strings.Contains(m, "arm64"); {
		case arm == (arch == "arm64"):
			return p, true
		case !arm && emulated == "":
			emulated = p
		}
	}
	return emulated, emulated != ""
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
					if exe, ok := npmExe(stem, p, runtime.GOARCH); ok {
						return exe, nil
					}
					return "", fmt.Errorf("%w: %w", state.ErrInvalid, &BatchFileError{Name: stem, Path: p})
				}
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s not found on PATH", state.ErrInvalid, name)
}
