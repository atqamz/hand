package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestUninstallListsRemovesAndKeeps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows rules are tested in internal/uninstall")
	}
	h := newHarness(t)
	home := t.TempDir()
	h.vars["HOME"] = home
	h.vars["XDG_DATA_HOME"] = filepath.Join(home, "data")
	h.vars["XDG_STATE_HOME"] = filepath.Join(home, "state")
	bin := t.TempDir()
	target := filepath.Join(bin, "hand")
	iroh := filepath.Join(bin, "hand-iroh")
	for _, p := range []string{target, iroh} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cli.UninstallHooks.Target = target
	t.Cleanup(func() { cli.UninstallHooks.Target = "" })

	out, errOut, code := h.run("uninstall", "--dry-run")
	if code != 0 || errOut != "" || !strings.Contains(out, "status: would remove") || !strings.Contains(out, iroh) || !strings.Contains(out, h.vars["SECONDHAND_HOME"]) {
		t.Fatalf("dry run: code=%d stderr=%q stdout=\n%s", code, errOut, out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("the dry run removed hand")
	}
	if out, errOut, code := h.run("uninstall", "now"); code != 2 || out != "" || !strings.Contains(errOut, "want 0 argument(s)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if out, errOut, code := h.run("uninstall"); code != 0 || errOut != "" || !strings.Contains(out, "status: removed") {
		t.Fatalf("code=%d stderr=%q stdout=\n%s", code, errOut, out)
	}
	for _, p := range []string{target, iroh} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s is still there", p)
		}
	}
	if out, _, code := h.run("uninstall"); code != 0 || !strings.Contains(out, "status: nothing to remove") {
		t.Fatalf("second run: code=%d\n%s", code, out)
	}
}
