package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAHarnessFoundOnlyAsABatchFileIsNeverStarted(t *testing.T) {
	fx := newDoctorFixture(t)
	fx.remove(exe(fx.tools, "codex"))
	shim := filepath.Join(fx.tools, "codex.cmd")
	fx.write(shim, "@exit 0\r\n")
	const reason = "a batch file; Hand does not start batch files"

	out, errOut, code := fx.h.run("doctor")
	if row := checkRows(t, out)["harness codex"]; code != 0 || row[0] != "warn" || !strings.Contains(row[1], "codex.cmd") || !strings.Contains(row[1], reason) || !strings.Contains(row[1], "install the standalone codex, or add the folder of codex.exe to PATH") {
		t.Fatalf("code=%d harness codex = %q, stderr=%q", code, row, errOut)
	}
	if _, errOut, code := fx.h.run("supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"); code != 2 || !strings.Contains(errOut, reason) || !strings.Contains(errOut, "codex.cmd") {
		t.Fatalf("supervisor start: code=%d stderr=%q", code, errOut)
	}
}
