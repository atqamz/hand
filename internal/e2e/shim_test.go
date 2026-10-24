package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexWindowsRefusesACmdShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files are only a Windows hazard")
	}
	sb := newSandbox(t)
	sb.init()
	sb.write(filepath.Join(sb.root, "brief.md"), "Say hello.")
	shim := filepath.Join(sb.root, "shim")
	sb.write(filepath.Join(shim, "codex.cmd"), "@exit 0\r\n")
	path := []string{shim}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "codex.exe")); err != nil {
			path = append(path, dir)
		}
	}
	sb.extraEnv = []string{"PATH=" + strings.Join(path, string(os.PathListSeparator))}
	for _, args := range [][]string{
		{"supervisor", "start", "--harness", "codex", "--model", "gpt-5", "--effort", "low"},
		{"attempt", "start", "--harness", "codex", "--model", "gpt-5", "--effort", "low", "--prompt-file", filepath.Join(sb.root, "brief.md"), "t1"},
	} {
		r := sb.hand(args...)
		want := "a batch file; Hand does not start batch files, because cmd.exe runs them in the wrong folder and mangles the prompt; install the standalone codex, or add the folder of codex.exe to PATH"
		if r.code != 2 || !strings.Contains(r.errOut, "codex is installed only as "+filepath.Join(shim, "codex.cmd")) || !strings.Contains(r.errOut, want) {
			t.Fatalf("hand %q: exit %d\n%s%s", args, r.code, r.out, r.errOut)
		}
	}
	if r := sb.hand("supervisor", "show"); r.code != 3 || !strings.Contains(r.errOut, "no supervisor") {
		t.Fatalf("a supervisor exists after the refusal: exit %d\n%s%s", r.code, r.out, r.errOut)
	}
}
