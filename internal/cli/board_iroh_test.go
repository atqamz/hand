package cli_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/sidecar"
)

const (
	irohID   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	relayURL = "https://relay.test./"
)

var appLinkRe = regexp.MustCompile(`handboard://[^"<\s]+`)

func redactAppPage(page string) string { return appLinkRe.ReplaceAllString(page, "handboard://…") }

func desktopMain(args []string) int {
	p := fakebin.Params()
	if len(args) == 1 && args[0] == "--version" {
		if p["raw"] != "" {
			fmt.Println(p["raw"])
			return 0
		}
		fmt.Println("hand-desktop " + p["version"] + " " + p["commit"])
		return 0
	}
	return 2
}

func sidecarMain(args []string) int {
	p := fakebin.Params()
	if len(args) == 1 && args[0] == "--version" {
		if p["raw"] != "" {
			fmt.Println(p["version"])
			return 0
		}
		fmt.Println("hand-iroh " + p["version"] + " iroh 1.4.1")
		return 0
	}
	var keys []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	fakebin.Append(p["log"], strings.Join(args, " ")+" | env "+strings.Join(keys, ","))
	if p["mode"] == "die" {
		fmt.Fprintln(os.Stderr, "hand-iroh: cannot open the endpoint: boom")
		return 1
	}
	if p["delay"] != "" {
		d, _ := time.ParseDuration(p["delay"])
		time.Sleep(d)
	}
	fmt.Println("ready id=" + irohID + " relay=" + relayURL)
	_, _ = io.Copy(io.Discard, os.Stdin)
	fakebin.Append(p["log"], "stdin closed")
	return 0
}

type irohFixture struct {
	h     *harness
	root  string
	log   string
	tools string
}

func newIrohFixture(t *testing.T, params map[string]string) *irohFixture {
	t.Helper()
	h := initWithProject(t)
	fx := &irohFixture{h: h, root: h.vars["SECONDHAND_HOME"], log: filepath.Join(t.TempDir(), "log"), tools: t.TempDir()}
	if params != nil {
		p := map[string]string{"log": fx.log, "version": sidecar.Version}
		for k, v := range params {
			p[k] = v
		}
		fakebin.Install(t, fx.tools, "hand-iroh", "sidecar", p)
	}
	h.vars["PATH"] = fx.tools
	return fx
}

func (fx *irohFixture) state() string {
	b, _ := os.ReadFile(filepath.Join(fx.root, "board.iroh"))
	return strings.TrimSpace(string(b))
}

func (fx *irohFixture) log0() string {
	b, _ := os.ReadFile(fx.log)
	return string(b)
}

func within(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func phonePage(t *testing.T, fleetURL, token string) string {
	t.Helper()
	req, err := http.NewRequest("GET", fleetURL+"/connect", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "hand_board_" + filepath.Base(fleetURL), Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the Phone page = %d:\n%s", res.StatusCode, b)
	}
	return string(b)
}

func TestBoardIrohStartsTheSidecarAsItsChildAndShowsItsState(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{})
	secret := ""
	base, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	fleetURL, token := fleetPage(t, base, fx.h)
	within(t, "ready", func() bool { return strings.HasPrefix(fx.state(), "ready "+irohID+" "+relayURL) })
	if b, err := os.ReadFile(filepath.Join(fx.root, "board.peer")); err != nil {
		t.Fatal(err)
	} else {
		secret = strings.TrimSpace(string(b))
	}
	page := phonePage(t, fleetURL, token)
	for _, want := range []string{"Phone access over iroh is online", irohID, "The Hand app can pair with this laptop", `data-tone="pass"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the Phone page lacks %q", want)
		}
	}
	if strings.Contains(page, secret) {
		t.Fatal("the Phone page shows the peer secret")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	id := filepath.Base(fleetURL)
	if csrf == nil {
		t.Fatal("the Phone page holds no form token")
	}
	req, err := http.NewRequest("POST", fleetURL+"/connect/app", strings.NewReader(url.Values{"csrf": {csrf[1]}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hand_board_" + id, Value: token})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	appPage, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(appPage), "handboard://c#v=1&amp;e="+irohID+"&amp;f="+id+"&amp;c=") || !strings.Contains(string(appPage), "&amp;r=https%3A%2F%2Frelay.test.%2F") || !strings.Contains(string(appPage), `<svg class="qr"`) {
		t.Fatalf("the Hand app link = %d:\n%s", res.StatusCode, redactAppPage(string(appPage)))
	}
	if got := fx.log0(); strings.Contains(got, secret) || strings.Contains(got, "stdin closed") {
		t.Fatalf("sidecar log while the board runs = %q", got)
	}
	out := stop()
	peer := field(out, "peer")
	first, _, _ := strings.Cut(fx.log0(), " | env ")
	want := fmt.Sprintf("--board %s --secret-file %s --key-dir %s", peer, filepath.Join(fx.root, "board.peer"), filepath.Join(fx.root, "iroh"))
	if peer == "" || first != want {
		t.Fatalf("sidecar argv = %q, want %q", first, want)
	}
	if field(out, "iroh") != "starting" || !strings.Contains(out, "hand doctor") || !strings.Contains(out, "board: iroh: ready, endpoint ID "+irohID) {
		t.Fatalf("board output:\n%s", out)
	}
	if got := fx.log0(); !strings.HasSuffix(strings.TrimSpace(got), "stdin closed") {
		t.Fatalf("shutdown did not close the sidecar's stdin: %q", got)
	}
	if got := fx.state(); got != "stopped" {
		t.Fatalf("board.iroh after shutdown = %q, want the opt-in kept as stopped", got)
	}
}

func TestBoardIrohGivesTheSidecarNoEnvironmentAndNoSecretInItsArguments(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{})
	fx.h.vars["HTTPS_PROXY"] = "http://user:pass@proxy.example"
	t.Setenv("HAND_IROH_CANARY", "leaked")
	_, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	within(t, "ready", func() bool { return strings.HasPrefix(fx.state(), "ready ") })
	stop()
	_, env, _ := strings.Cut(strings.SplitN(fx.log0(), "\n", 2)[0], " | env ")
	for _, key := range strings.Split(env, ",") {
		if key != "" && !strings.EqualFold(key, "SystemRoot") {
			t.Errorf("the sidecar inherited %q", key)
		}
	}
}

func TestBoardIrohRecordsTheSettingAndABoardWithoutItClearsIt(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{})
	path := filepath.Join(fx.root, "board.iroh")
	_, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	within(t, "ready", func() bool { return strings.HasPrefix(fx.state(), "ready ") })
	stop()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a board started with --iroh left no record: %v", err)
	}
	before := len(strings.Split(strings.TrimSpace(fx.log0()), "\n"))
	base, stop := startBoard(t, fx.h, "127.0.0.1")
	fleetURL, token := fleetPage(t, base, fx.h)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a board started without --iroh kept the record: %v", err)
	}
	if page := phonePage(t, fleetURL, token); strings.Contains(page, "iroh") {
		t.Fatalf("the Phone page mentions iroh although it is off")
	}
	if out := stop(); strings.Contains(out, "iroh") || strings.Contains(out, "peer:") {
		t.Fatalf("a board without --iroh printed:\n%s", out)
	}
	if got := len(strings.Split(strings.TrimSpace(fx.log0()), "\n")); got != before {
		t.Fatalf("a board without --iroh started the sidecar again")
	}
}

func TestBoardIrohWithNoSidecarStillServesAndSaysWhyItIsOff(t *testing.T) {
	fx := newIrohFixture(t, nil)
	base, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	fleetURL, token := fleetPage(t, base, fx.h)
	within(t, "failed", func() bool { return strings.HasPrefix(fx.state(), "failed ") })
	if got := fx.state(); !strings.Contains(got, "hand-iroh not found: it is not next to hand") || !strings.Contains(got, "or on PATH") {
		t.Fatalf("board.iroh = %q", got)
	}
	page := phonePage(t, fleetURL, token)
	for _, want := range []string{`data-tone="fail"`, "Phone access over iroh is hand-iroh not found"} {
		if !strings.Contains(page, want) {
			t.Errorf("the Phone page lacks %q", want)
		}
	}
	if out := stop(); !strings.Contains(out, "board: iroh: failed: hand-iroh not found") {
		t.Fatalf("board output:\n%s", out)
	}
}

func TestBoardIrohRefusesASidecarOfAnotherVersion(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{"version": "9.9.9"})
	_, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	within(t, "failed", func() bool { return strings.HasPrefix(fx.state(), "failed ") })
	if got := fx.state(); !strings.Contains(got, "is 9.9.9, but this hand needs "+sidecar.Version) {
		t.Fatalf("board.iroh = %q", got)
	}
	stop()
	if got := fx.log0(); got != "" {
		t.Fatalf("a sidecar of the wrong version was started: %q", got)
	}
}

func TestBoardIrohRestartsAnExitedSidecarAndSaysWhy(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{"mode": "die"})
	base, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	fleetURL, token := fleetPage(t, base, fx.h)
	within(t, "a restart", func() bool { return strings.Count(fx.log0(), "\n") >= 2 })
	if got := fx.state(); !strings.HasPrefix(got, "starting exit status 1: hand-iroh: cannot open the endpoint: boom") && !strings.HasPrefix(got, "failed ") {
		t.Fatalf("board.iroh = %q", got)
	}
	if page := phonePage(t, fleetURL, token); !strings.Contains(page, "restarting after: exit status 1: hand-iroh: cannot open the endpoint: boom") {
		t.Fatalf("the Phone page does not say why the sidecar restarts:\n%s", page)
	}
	if out := stop(); !strings.Contains(out, "board: iroh: restarting after: exit status 1") {
		t.Fatalf("board output:\n%s", out)
	}
}

func TestBoardIrohReportsAStateWriteFailure(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{"delay": "2s"})
	base, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	within(t, "the starting state", func() bool { return fx.state() == "starting" })
	fi, err := os.Stat(fx.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fx.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(fx.root, fi.Mode().Perm()); err != nil {
			t.Errorf("restore %s: %v", fx.root, err)
		}
	})
	// Write bits stop the board's next write without racing the file itself;
	// root ignores them, so skip where a probe write still succeeds.
	if probe, err := os.CreateTemp(fx.root, ".write-probe-"); err == nil {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		t.Skip("this test needs directory permissions to deny the board's writes")
	}
	time.Sleep(3 * time.Second)
	fleetPage(t, base, fx.h)
	out := stop()
	if !strings.Contains(out, "board: iroh: cannot record state:") {
		t.Fatalf("board output:\n%s", out)
	}
}

func TestBoardIrohStopsShowingReadyWhenThePeerListenerFails(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{})
	old := *cli.ServePeer
	*cli.ServePeer = func(*board.Peer, net.Listener) error { return errors.New("peer listener failed") }
	t.Cleanup(func() { *cli.ServePeer = old })
	base, stop := startBoard(t, fx.h, "127.0.0.1", "--iroh")
	fleetURL, token := fleetPage(t, base, fx.h)
	within(t, "stopped", func() bool { return fx.state() == "stopped" })
	doc, _, _ := fx.h.run("doctor")
	row := checkRows(t, doc)["iroh"]
	if row[0] != "info" || !strings.Contains(row[1], "stopped") {
		t.Fatalf("iroh row = %q", row)
	}
	if page := phonePage(t, fleetURL, token); strings.Contains(page, irohID) || strings.Contains(page, "Phone access over iroh is online") {
		t.Fatalf("the Phone page still shows the sidecar ready")
	}
	out := stop()
	if !strings.Contains(out, "board: the peer listener stopped: peer listener failed") {
		t.Fatalf("board output:\n%s", out)
	}
}

func TestBoardIrohNeedsALoopbackBoard(t *testing.T) {
	fx := newIrohFixture(t, map[string]string{})
	errOut, code := boardRefusal(fx.h, "--addr", "0.0.0.0:0", "--iroh")
	if code != 2 || !strings.Contains(errOut, "board: --iroh needs --addr on a loopback address") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(fx.root, "board.iroh")); !os.IsNotExist(err) {
		t.Fatalf("a refused start recorded the setting: %v", err)
	}
}
