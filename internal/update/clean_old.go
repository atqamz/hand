package update

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/sidecar"
)

func cleanOld(exe string) {
	dir := filepath.Dir(exe)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		for _, name := range []string{filepath.Base(exe), sidecar.Name + ".exe", desktopasset.Name + ".exe"} {
			if strings.HasPrefix(e.Name(), name+".old") {
				_ = os.Remove(filepath.Join(dir, e.Name()))
				break
			}
		}
	}
}
