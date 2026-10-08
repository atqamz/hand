//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSystemctl(t *testing.T, fail map[string]bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	var fails []string
	for prefix := range fail {
		fails = append(fails, prefix)
	}
	if err := os.WriteFile(filepath.Join(dir, "fail.txt"), []byte(strings.Join(fails, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"systemctl $*\" >> " + calls + "\n" +
		"while IFS= read -r f; do if [ -n \"$f\" ]; then case \"$2 $3\" in \"$f\"*) echo \"Failed to $2 $3: boom\" >&2; exit 1 ;; esac; fi; done < " + filepath.Join(dir, "fail.txt") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func pathEnv(dir string) []string {
	return []string{"PATH=" + dir + ":" + os.Getenv("PATH")}
}
