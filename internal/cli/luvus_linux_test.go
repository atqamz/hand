package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

func fakeUserManager(t *testing.T, h *harness, pid int) {
	t.Helper()
	dir := filepath.SplitList(h.vars["PATH"])[0]
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\nexit 1\n",
		"systemctl":   "#!/bin/sh\necho " + strconv.Itoa(pid) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
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
	h.vars["XDG_RUNTIME_DIR"] = run
}

func TestLuvusShowMatchesTheServerByContent(t *testing.T) {
	h := newHarness(t)
	h.vars["PATH"] = t.TempDir()
	fakeUserManager(t, h, os.Getpid())
	h.ok("init")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	pin := `{"path":"/elsewhere/luvus","sha256":"` + hex.EncodeToString(sum[:]) + `","version":"0.14.2","source":"/usr/bin/luvus","pinned_at":"2026-10-01T00:00:00Z"}`
	if err := os.MkdirAll(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json"), []byte(pin), 0o644); err != nil {
		t.Fatal(err)
	}
	has(t, "show", h.ok("luvus", "show"), "server: "+self, "match: yes")
}

func TestLuvusShowReadsTheRunningServer(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	fakeUserManager(t, h, os.Getpid())
	h.ok("init")
	pin, _, err := luvus.LoadPin(h.vars["SECONDHAND_HOME"])
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	has(t, "show", h.ok("luvus", "show"), "pin: 0.14.3 "+pin.Path, "server: "+self, "match: no", "systemctl --user stop secondhand-luvus-")
	has(t, "pin", h.ok("luvus", "pin", fakeLuvusAt(t, t.TempDir(), "0.14.4")), "server: "+self, "match: no", "`hand attach` refuses a server of another version", "until the server restarts")
}
