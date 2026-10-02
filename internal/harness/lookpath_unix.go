//go:build unix

package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/state"
)

func LookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s not found on PATH", state.ErrInvalid, name)
}
