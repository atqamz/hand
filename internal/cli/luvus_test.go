package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/toon"
)

func fakeLuvusAt(t *testing.T, dir, version string) string {
	t.Helper()
	return embedAt(t, dir, "luvus", map[string]string{"on --version": "luvus " + version + "\n"})
}

func embedAt(t *testing.T, dir, name string, params map[string]string) string {
	t.Helper()
	bin := exe(dir, name)
	if err := os.WriteFile(bin, fakebin.Embed(t, "fake", params), 0o755); err != nil {
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
	if p, err := strconv.Unquote(m[1]); err == nil {
		return p
	}
	return m[1]
}

func TestLuvusPinCopiesTheBinaryOnPath(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"], h.cwd = "", t.TempDir()
	script := pinnable(t, h, "0.14.3")
	out := h.ok("luvus", "pin")
	has(t, "first pin", out, "version: 0.14.3", "source: "+toon.Value(script), "previous: none")
	first := pinnedPath(t, out)
	if !strings.HasPrefix(first, filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "0.14.3-")) {
		t.Fatalf("first pin path = %s", first)
	}
	other := fakeLuvusAt(t, t.TempDir(), "0.14.4")
	has(t, "second pin", h.ok("luvus", "pin", other), "version: 0.14.4", "source: "+toon.Value(other), "previous: "+toon.Value("0.14.3 "+first))
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
	hello := embedAt(t, t.TempDir(), "hello", map[string]string{"on --version": "hello\n"})
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
	if err := os.Remove(pinned); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinned, []byte("tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.tty = true
	if _, errOut, code := h.run("attach", "supervisor"); code != 3 || !strings.Contains(errOut, "changed on disk; re-pin with") || len(h.exec) != 0 {
		t.Fatalf("code=%d stderr=%q exec=%q", code, errOut, h.exec)
	}
}

func TestTheServerRefusesAChangedPin(t *testing.T) {
	h := initWithProject(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	fakebin.Install(t, bin, "systemd-run", "fake", map[string]string{"log": calls, "exit": "1"})
	fakebin.Install(t, bin, "systemctl", "fake", nil)
	embedAt(t, bin, "luvus", map[string]string{"on --version": "luvus 0.14.3\n", "log": calls, "prefix": "LUVUS-SPAWNED-DIRECTLY", "exit": "1"})
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
	if out := h.ok("init"); !strings.HasPrefix(field(out, "luvus"), "pinned 0.14.3 ("+filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "0.14.3-")) {
		t.Fatalf("first init = %q", out)
	}
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
	embedAt(t, broken.vars["PATH"], "luvus", map[string]string{"exit": "1"})
	has(t, "broken luvus", broken.ok("init"), "Luvus was not pinned (")
	if names, _ := os.ReadDir(filepath.Join(broken.vars["SECONDHAND_HOME"], "luvus")); slices.ContainsFunc(names, func(e os.DirEntry) bool { return e.Name() == "pin.json" }) {
		t.Fatal("a broken luvus was pinned")
	}
}

func TestServerMatch(t *testing.T) {
	pin := luvus.Pin{Version: "0.14.3"}
	for _, c := range []struct {
		pinned        bool
		version       string
		err           error
		server, match string
	}{
		{true, "0.14.3", nil, "0.14.3", "yes"},
		{true, "0.14.2", nil, "0.14.2", "no"},
		{false, "0.14.3", nil, "0.14.3", "unknown"},
		{true, "", nil, "unknown", "unknown"},
		{true, "", luvus.ErrUnreachable, "not running", "not running"},
		{true, "", errors.New("timeout"), "unknown", "unknown"},
	} {
		if server, match := cli.ServerMatch(pin, c.pinned, c.version, c.err); server != c.server || match != c.match {
			t.Fatalf("%+v: got %q %q", c, server, match)
		}
	}
}

func TestLuvusShowPrintsTheServerVersion(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	h.ok("init")
	sock := filepath.Join(t.TempDir(), "uhp.sock")
	h.vars["HAND_LUVUS_SOCKET"] = sock
	has(t, "not running", h.ok("luvus", "show"), "server: not running", "match: not running")
	srv := fakeuhp.Start(t, sock)
	reply := func(version string) {
		srv.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"type": "pong", "version": version}, nil })
	}
	reply("0.14.3")
	has(t, "match", h.ok("luvus", "show"), "server: 0.14.3", "match: yes")
	reply("")
	has(t, "unknown", h.ok("luvus", "show"), "server: unknown", "match: unknown")
	reply("0.14.2")
	has(t, "stale", h.ok("luvus", "show"), "server: 0.14.2", "match: no", "run `hand update`")
	has(t, "pin", h.ok("luvus", "pin", fakeLuvusAt(t, t.TempDir(), "0.14.4")), "server: 0.14.2", "match: no", "`hand attach` refuses a server of another version", "until the server restarts")
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
