package cli

import (
	"os"
	"path/filepath"
	"strings"
)

var vcRuntimeDLLs = []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"}

func (r *runner) doctorVCRuntime(c *checks) {
	root := r.env.Getenv("SystemRoot")
	if root == "" {
		return
	}
	missing := missingDLLs(filepath.Join(root, "System32"))
	if len(missing) == 0 {
		c.add("visual c++ runtime", "ok", "")
		return
	}
	c.add("visual c++ runtime", "warn", strings.Join(missing, ", ")+" missing, and luvus.exe needs them; run `winget install Microsoft.VCRedist.2015+.x64` (or open https://aka.ms/vs/17/release/vc_redist.x64.exe), then `hand luvus pin`")
}

func missingDLLs(dir string) []string {
	var missing []string
	for _, name := range vcRuntimeDLLs {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}
