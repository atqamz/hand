//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTheServerStartsFromThePin(t *testing.T) {
	h := initWithProject(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\necho \"$*\" >> " + calls + "\necho 'Failed to start transient service unit' >&2\nexit 1\n",
		"systemctl":   "#!/bin/sh\nexit 0\n",
		"luvus":       "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'luvus 0.14.3'; exit 0; fi\necho \"LUVUS-SPAWNED-DIRECTLY $*\" >> " + calls + "\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := t.TempDir()
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.vars["PATH"], h.vars["XDG_RUNTIME_DIR"] = bin, run
	h.vars["HAND_LUVUS_SOCKET"] = filepath.Join(t.TempDir(), "absent.sock")
	pinned := pinnedPath(t, h.ok("luvus", "pin"))
	id := regexp.MustCompile(`\((f[0-9a-f]{12})\)`).FindStringSubmatch(h.ok("orient"))[1]
	h.run("attempt", "list")
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), pinned+" --session secondhand-"+id+" server start") || strings.Contains(string(got), filepath.Join(bin, "luvus")) {
		t.Fatalf("calls = %q", got)
	}
}

func TestLuvusStartsInTheFleetsOwnUserUnit(t *testing.T) {
	h := initWithProject(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\necho \"$*\" >> " + calls + "\necho 'Failed to start transient service unit' >&2\nexit 1\n",
		"systemctl":   "#!/bin/sh\nexit 0\n",
		"luvus":       "#!/bin/sh\necho \"LUVUS-SPAWNED-DIRECTLY $*\" >> " + calls + "\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := t.TempDir()
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.vars["PATH"], h.vars["XDG_RUNTIME_DIR"] = bin, run
	h.vars["HAND_LUVUS_SOCKET"] = filepath.Join(t.TempDir(), "absent.sock")
	id := regexp.MustCompile(`\((f[0-9a-f]{12})\)`).FindStringSubmatch(h.ok("orient"))[1]
	if _, errOut, code := h.run("attempt", "list"); code == 0 || !strings.Contains(errOut, "secondhand-luvus-"+id) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), "--unit=secondhand-luvus-"+id+" ") || strings.Contains(string(got), "LUVUS-SPAWNED-DIRECTLY") {
		t.Fatalf("calls = %q", got)
	}
}
