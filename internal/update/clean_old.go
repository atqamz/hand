package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/sidecar"
)

var oldSuffix = regexp.MustCompile(`^\.old(-[A-Z2-7]{26})?$`)

func cleanOld(exe string) {
	dir := filepath.Dir(exe)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, name := range []string{filepath.Base(exe), sidecar.Name + ".exe", desktopasset.Name + ".exe"} {
			if rest, ok := strings.CutPrefix(e.Name(), name); ok && oldSuffix.MatchString(rest) {
				_ = os.Remove(filepath.Join(dir, e.Name()))
				break
			}
		}
	}
}
