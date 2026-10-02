package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
)

func fakeLuvusAt(t *testing.T, dir, version string) string {
	t.Helper()
	bin := filepath.Join(dir, "luvus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'luvus "+version+"'; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func pinnable(t *testing.T, h *harness, version string) string {
	t.Helper()
	if h.vars["PATH"] == "" {
		h.vars["PATH"] = t.TempDir()
	}
	return fakeLuvusAt(t, filepath.SplitList(h.vars["PATH"])[0], version)
}

func pinnedPath(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^path: (.+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no path in %q", out)
	}
	return m[1]
}

func TestLuvusPinCopiesTheBinaryOnPath(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"], h.cwd = "", t.TempDir()
	script := pinnable(t, h, "0.14.3")
	out := h.ok("luvus", "pin")
	has(t, "first pin", out, "version: 0.14.3", "source: "+script, "previous: none", "path: "+filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "0.14.3-"))
	first := pinnedPath(t, out)
	other := fakeLuvusAt(t, t.TempDir(), "0.14.4")
	has(t, "second pin", h.ok("luvus", "pin", other), "version: 0.14.4", "source: "+other, "previous: 0.14.3 "+first)
}

func TestLuvusPinRefusals(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"], h.cwd = "", t.TempDir()
	pinnable(t, h, "0.14.3")
	current := pinnedPath(t, h.ok("luvus", "pin"))
	pinFile := filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json")
	before, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatal(err)
	}
	hello := filepath.Join(t.TempDir(), "hello")
	if err := os.WriteFile(hello, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ bin, want string }{
		{filepath.Join(t.TempDir(), "missing"), "not a luvus binary"},
		{hello, "not a luvus binary"},
		{current, "already the pinned copy"},
	} {
		if _, errOut, code := h.run("luvus", "pin", c.bin); code != 2 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%s: code=%d stderr=%q", c.bin, code, errOut)
		}
	}
	if after, err := os.ReadFile(pinFile); err != nil || string(after) != string(before) {
		t.Fatalf("pin.json changed: %v", err)
	}
}

func TestAttachUsesThePinnedLuvus(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	pinnable(t, h, "0.14.3")
	pinned := pinnedPath(t, h.ok("luvus", "pin"))
	h.tty = true
	h.ok("attach", "supervisor")
	if len(h.exec) != 1 || h.exec[0][0] != pinned || h.exec[0][1] != pinned {
		t.Fatalf("exec = %q, want %s", h.exec, pinned)
	}
}

func TestAChangedPinIsRefused(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	pinnable(t, h, "0.14.3")
	pinned := pinnedPath(t, h.ok("luvus", "pin"))
	if err := os.Chmod(pinned, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.tty = true
	if _, errOut, code := h.run("attach", "supervisor"); code != 3 || !strings.Contains(errOut, "changed on disk; re-pin with") || len(h.exec) != 0 {
		t.Fatalf("code=%d stderr=%q exec=%q", code, errOut, h.exec)
	}
}

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

func TestTheServerRefusesAChangedPin(t *testing.T) {
	h := initWithProject(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\necho \"$*\" >> " + calls + "\nexit 1\n",
		"systemctl":   "#!/bin/sh\nexit 0\n",
		"luvus":       "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'luvus 0.14.3'; exit 0; fi\necho LUVUS-SPAWNED-DIRECTLY >> " + calls + "\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.vars["PATH"] = bin
	h.vars["HAND_LUVUS_SOCKET"] = filepath.Join(t.TempDir(), "absent.sock")
	pinned := pinnedPath(t, h.ok("luvus", "pin"))
	if err := os.Remove(pinned); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("attempt", "list"); code != 3 || !strings.Contains(errOut, "changed on disk; re-pin with") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got, _ := os.ReadFile(calls); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestInitPinsTheLuvusOnPath(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	has(t, "first init", h.ok("init"), "luvus: pinned 0.14.3 ("+filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "0.14.3-"))
	pinFile := filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json")
	before, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(1e12)
	has(t, "second init", h.ok("init"), "luvus: 0.14.3 (pinned)")
	if after, err := os.ReadFile(pinFile); err != nil || string(after) != string(before) {
		t.Fatalf("the second init re-pinned: %v", err)
	}
}

func TestInitWithoutLuvusStillSucceeds(t *testing.T) {
	h := newHarness(t)
	h.vars["PATH"] = t.TempDir()
	out := h.ok("init")
	has(t, "no luvus", out, "Install Luvus, then pin it: `hand luvus pin`")
	if strings.Contains(out, "luvus:") {
		t.Fatalf("init = %q", out)
	}
	broken := newHarness(t)
	broken.vars["PATH"] = t.TempDir()
	if err := os.WriteFile(filepath.Join(broken.vars["PATH"], "luvus"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	has(t, "broken luvus", broken.ok("init"), "Luvus was not pinned (")
	if names, _ := os.ReadDir(filepath.Join(broken.vars["SECONDHAND_HOME"], "luvus")); slices.ContainsFunc(names, func(e os.DirEntry) bool { return e.Name() == "pin.json" }) {
		t.Fatal("a broken luvus was pinned")
	}
}

func TestServerMatch(t *testing.T) {
	pin := luvus.Pin{Path: "/s/luvus/0.14.2-abcdef12/luvus", SHA256: "abcdef12aa"}
	for _, c := range []struct {
		pinned, known bool
		srv           luvus.Server
		want          string
	}{
		{false, true, luvus.Server{Exe: pin.Path, SHA256: pin.SHA256}, "unknown"},
		{true, false, luvus.Server{}, "unknown"},
		{true, true, luvus.Server{Exe: "/usr/bin/luvus"}, "unknown"},
		{true, true, luvus.Server{Exe: "/usr/bin/luvus", SHA256: pin.SHA256}, "yes"},
		{true, true, luvus.Server{Exe: "/usr/bin/luvus (deleted)", SHA256: pin.SHA256}, "yes"},
		{true, true, luvus.Server{Exe: "/usr/bin/luvus (deleted)", SHA256: "0123"}, "no"},
		{true, true, luvus.Server{Exe: pin.Path, SHA256: "0123"}, "no"},
	} {
		if got := cli.ServerMatch(pin, c.pinned, c.srv, c.known); got != c.want {
			t.Fatalf("%+v: got %q", c, got)
		}
	}
}

func TestLuvusPinInAMovedFleetLeavesThePinAlone(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	h.ok("init")
	pinFile := filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json")
	before, err := os.ReadFile(pinFile)
	if err != nil {
		t.Fatal(err)
	}
	old := h.home
	h.home = filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(old, h.home); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("luvus", "pin", fakeLuvusAt(t, t.TempDir(), "0.14.4")); code != 3 || !strings.Contains(errOut, "moved here from") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if after, err := os.ReadFile(pinFile); err != nil || string(after) != string(before) {
		t.Fatalf("pin.json changed: %v", err)
	}
}

func TestLuvusShowReportsABadHome(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run("--home", filepath.Join(t.TempDir(), "typo"), "luvus", "show"); code != 3 {
		t.Fatalf("code = %d", code)
	}
}

func TestAnUnreadablePinCanBeReplaced(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	if err := os.MkdirAll(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.tty = true
	if _, errOut, code := h.run("attach", "supervisor"); code != 3 || !strings.Contains(errOut, "re-pin with") {
		t.Fatalf("attach code=%d stderr=%q", code, errOut)
	}
	pinnable(t, h, "0.14.3")
	has(t, "re-pin", h.ok("luvus", "pin"), "version: 0.14.3", "previous: unreadable")
}

func TestLuvusShowOutsideAFleet(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"], h.cwd = "", t.TempDir()
	out := h.ok("luvus", "show")
	has(t, "outside", out, "pin: none")
	if strings.Contains(out, "server:") {
		t.Fatalf("show = %q", out)
	}
}

func TestLuvusShowWithoutAUserManager(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	h.ok("init")
	has(t, "show", h.ok("luvus", "show"), "pin: 0.14.3 ", "server: unknown", "match: unknown")
}

func TestLuvusShowRefusesAnUnreadablePin(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	if err := os.MkdirAll(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("luvus", "show"); code != 3 || !strings.Contains(errOut, "re-pin with") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}
