package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
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
		if err := fakebin.WriteExecutable(p, []byte("x")); err != nil {
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

func TestUninstallNeverCreatesTheFleetRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows rules are tested in internal/uninstall")
	}
	h := newHarness(t)
	home := t.TempDir()
	delete(h.vars, "SECONDHAND_HOME")
	h.vars["HOME"] = home
	h.vars["XDG_DATA_HOME"] = filepath.Join(home, "data")
	h.vars["XDG_STATE_HOME"] = filepath.Join(home, "state")
	target := filepath.Join(t.TempDir(), "hand")
	if err := fakebin.WriteExecutable(target, []byte("x")); err != nil {
		t.Fatal(err)
	}
	cli.UninstallHooks.Target = target
	t.Cleanup(func() { cli.UninstallHooks.Target = "" })
	listing := func() string {
		var lines []string
		_ = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
			lines = append(lines, p)
			return nil
		})
		return strings.Join(lines, "\n")
	}
	before := listing()
	for _, args := range [][]string{{"uninstall", "--dry-run"}, {"uninstall"}} {
		out, errOut, code := h.run(args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: code=%d stderr=%q", args, code, errOut)
		}
		if strings.Contains(out, ".secondhand") {
			t.Fatalf("%v lists a state folder that does not exist:\n%s", args, out)
		}
		if after := listing(); after != before {
			t.Fatalf("%v changed the home:\nbefore\n%s\nafter\n%s", args, before, after)
		}
	}
}
