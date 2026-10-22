package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

func tailscaleMain(args []string) int {
	p := fakebin.Params()
	if p["log"] != "" {
		fakebin.Append(p["log"], strings.Join(args, " "))
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	if p["exit"] != "" {
		fmt.Fprint(os.Stderr, p["stderr"])
		return 1
	}
	if len(args) > 0 {
		fmt.Print(p[args[0]])
	}
	return 0
}

const phoneName = "laptop.tailnet.example"

func serveFor(addr, path string) string {
	return `{"TCP":{"443":{"HTTPS":true}},"Web":{"` + phoneName + `:443":{"Handlers":{"` + path + `":{"Proxy":"http://` + addr + `"}}}}}`
}

func funnelFor(addr string) string {
	return `{"Web":{"` + phoneName + `:443":{"Handlers":{"/":{"Proxy":"http://` + addr + `"}}}},"AllowFunnel":{"` + phoneName + `:443":true}}`
}

func installTailscale(t *testing.T, dir string, params map[string]string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "tailscale.log")
	params["log"] = log
	fakebin.Install(t, dir, "tailscale", "tailscale", params)
	return log
}

func readOnly(t *testing.T, log string) {
	t.Helper()
	b, _ := os.ReadFile(log)
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "serve status --json" && l != "status --json" {
			t.Fatalf("tailscale ran %q, want only the two read-only status commands: %q", l, b)
		}
	}
}

func TestOpenPrintUsesThePhoneAddressOnlyWhenServeProxiesTheBoardRoot(t *testing.T) {
	old := *cli.PhoneProbe
	*cli.PhoneProbe = 300 * time.Millisecond
	t.Cleanup(func() { *cli.PhoneProbe = old })
	fx := newOpenFixture(t)
	addr := strings.TrimPrefix(fx.base, "http://")
	before := fx.h.ok("open", "--print", "t1")
	local := fx.base + "/" + fx.id + "/task/t1?token=" + fx.token(t)
	if field(before, "url") != local {
		t.Fatalf("open --print with no tailscale = %q", before)
	}
	phone := "https://" + phoneName + "/" + fx.id + "/task/t1?token=" + fx.token(t)
	status := `{"CertDomains":["` + phoneName + `"],"Peer":{}}`
	for _, c := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"root proxy", map[string]string{"serve": serveFor(addr, "/"), "status": status}, phone},
		{"no status answer needed", map[string]string{"serve": serveFor(addr, "/"), "status": "<html>"}, phone},
		{"path mount", map[string]string{"serve": serveFor(addr, "/board"), "status": status}, local},
		{"funnel", map[string]string{"serve": funnelFor(addr), "status": status}, local},
		{"another port", map[string]string{"serve": serveFor("127.0.0.1:1", "/"), "status": status}, local},
		{"nothing served", map[string]string{"serve": "{}", "status": status}, local},
		{"garbage", map[string]string{"serve": "<html>serve</html>", "status": "<html>"}, local},
		{"not an object", map[string]string{"serve": "[1]", "status": status}, local},
		{"exit 1", map[string]string{"exit": "1", "stderr": "failed to connect to local tailscaled"}, local},
		{"hang", map[string]string{"sleep": "30s", "serve": serveFor(addr, "/")}, local},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := fx.h.vars["PATH"]
			log := installTailscale(t, dir, c.params)
			start := time.Now()
			out, errOut, code := fx.h.run("open", "--print", "t1")
			if time.Since(start) > 10*time.Second {
				t.Fatalf("open --print took %v", time.Since(start))
			}
			if code != 0 || errOut != "" || field(out, "url") != c.want || !strings.Contains(out, "The link holds the fleet's token; paste it only into your own browser") {
				t.Fatalf("open --print: code=%d out=%q stderr=%q, want url %q", code, out, errOut, strings.ReplaceAll(c.want, fx.token(t), "TOKEN"))
			}
			readOnly(t, log)
		})
	}
}

func TestOpenWithoutPrintKeepsTheLocalAddressWhateverServeSays(t *testing.T) {
	fx := newOpenFixture(t)
	addr := strings.TrimPrefix(fx.base, "http://")
	log := installTailscale(t, fx.h.vars["PATH"], map[string]string{"serve": serveFor(addr, "/")})
	out := fx.h.ok("open", "t1")
	if field(out, "opened") != fx.base+"/"+fx.id+"/task/t1" {
		t.Fatalf("open = %q", out)
	}
	if got := fx.opened(t); len(got) != 1 || !strings.HasPrefix(got[0], fx.base+"/"+fx.id+"/task/t1?token=") {
		t.Fatalf("browser opened %q", got)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("hand open ran tailscale: %q", b)
	}
}

func TestOpenPrintNeverPrintsTheTokenAnywhereNew(t *testing.T) {
	fx := newOpenFixture(t)
	addr := strings.TrimPrefix(fx.base, "http://")
	installTailscale(t, fx.h.vars["PATH"], map[string]string{"serve": serveFor(addr, "/")})
	out, errOut, _ := fx.h.run("open", "--print", "t1")
	if n := strings.Count(out+errOut, fx.token(t)); n != 1 {
		t.Fatalf("the token appears %d times in %q %q", n, out, errOut)
	}
}

func TestWatchAlertsLinkThePhoneAddressAndAskTailscaleOncePerHalfMinute(t *testing.T) {
	fx := newAttemptFixture(t)
	log := filepath.Join(t.TempDir(), "notify.log")
	fakebin.Install(t, fx.h.vars["PATH"], "hand-notify", "notifyenv", map[string]string{"log": log})
	tsLog := installTailscale(t, fx.h.vars["PATH"], map[string]string{"serve": serveFor("127.0.0.1:7777", "/")})
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"notify":["hand-notify"]}`
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), []byte("127.0.0.1:7777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := startWatch(t, fx, "--every", "50ms")
	defer stop()
	lines := func() []string {
		b, _ := os.ReadFile(log)
		if len(b) == 0 {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	fx.h.ok("decision", "ask", "t1", "First question?")
	eventually(t, func() bool { return len(lines()) == 1 })
	fx.h.ok("decision", "ask", "t1", "Second question?")
	eventually(t, func() bool { return len(lines()) == 2 })
	for i, want := range []string{"d1", "d2"} {
		if got := lines()[i]; !strings.HasSuffix(got, "|https://"+phoneName+"/"+field(fx.h.ok("init"), "id")+"/decision/"+want) {
			t.Fatalf("alert %d = %q", i, got)
		}
	}
	readOnly(t, tsLog)
	if n := strings.Count(strings.TrimSpace(string(mustRead(t, tsLog))), "\n") + 1; n != 2 {
		t.Fatalf("tailscale ran %d times for two alerts, want 2 (one per command)", n)
	}
	fx.h.advance(31 * time.Second)
	fx.h.ok("decision", "ask", "t1", "Third question?")
	eventually(t, func() bool { return len(lines()) == 3 })
	if n := strings.Count(strings.TrimSpace(string(mustRead(t, tsLog))), "\n") + 1; n != 4 {
		t.Fatalf("tailscale ran %d times after the cache expired, want 4", n)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWatchAlertKeepsTheLocalAddressWhenServeSaysFunnelOrNothing(t *testing.T) {
	for name, serve := range map[string]string{
		"funnel":  funnelFor("127.0.0.1:7777"),
		"nothing": "{}",
		"path":    serveFor("127.0.0.1:7777", "/b"),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			log := filepath.Join(t.TempDir(), "notify.log")
			fakebin.Install(t, fx.h.vars["PATH"], "hand-notify", "notifyenv", map[string]string{"log": log})
			installTailscale(t, fx.h.vars["PATH"], map[string]string{"serve": serve})
			policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"notify":["hand-notify"]}`
			if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte(policy), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), []byte("127.0.0.1:7777\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			stop := startWatch(t, fx, "--every", "50ms")
			defer stop()
			fx.h.ok("decision", "ask", "t1", "Question?")
			eventually(t, func() bool { b, _ := os.ReadFile(log); return len(b) > 0 })
			b, _ := os.ReadFile(log)
			if !strings.Contains(string(b), "|http://127.0.0.1:7777/") {
				t.Fatalf("alert = %q", b)
			}
		})
	}
}

func TestDoctorPhoneRowNamesWhatTailscaleFoundAndTheFix(t *testing.T) {
	for _, c := range []struct {
		name   string
		serve  func(addr string) string
		params map[string]string
		level  string
		fix    string
		exit   int
		phone  bool
	}{
		{"serve proxies the root", func(a string) string { return serveFor(a, "/") }, nil, "ok", "", 0, true},
		{"under a path", func(a string) string { return serveFor(a, "/board") }, nil, "warn", "under /board; the Board needs the root; run `tailscale serve --bg ", 0, false},
		{"funnel on", func(a string) string {
			return funnelFor(a)
		}, nil, "fail", "run `tailscale funnel reset`", 1, false},
		{"not set up", func(string) string { return "{}" }, nil, "info", "tailscale serve does not proxy the board yet; open Phone in the board for the steps, or run `tailscale serve --bg ", 0, false},
		{"another port", func(string) string { return serveFor("127.0.0.1:1", "/") }, nil, "info", "does not proxy the board yet", 0, false},
		{"garbage", func(string) string { return "<html>" }, nil, "info", "no tailscale answered", 0, false},
		{"exit 1", nil, map[string]string{"exit": "1", "stderr": "tailscaled is not running"}, "info", "no tailscale answered", 0, false},
		{"hang", nil, map[string]string{"sleep": "30s"}, "info", "no tailscale answered", 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := *cli.PhoneProbe
			*cli.PhoneProbe = 300 * time.Millisecond
			t.Cleanup(func() { *cli.PhoneProbe = old })
			fx := newDoctorFixture(t)
			addr, err := os.ReadFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"))
			if err != nil {
				t.Fatal(err)
			}
			params := c.params
			if params == nil {
				params = map[string]string{"serve": c.serve(strings.TrimSpace(string(addr))), "status": `{"CertDomains":["` + phoneName + `"]}`}
			}
			log := installTailscale(t, fx.tools, params)
			out, errOut, code := fx.h.run("doctor")
			row := checkRows(t, out)["phone"]
			if code != c.exit || row[0] != c.level || !strings.Contains(row[1], c.fix) {
				t.Fatalf("code=%d phone=%q stderr=%q out=%q", code, row, errOut, out)
			}
			if got := field(out, "phone"); c.phone != (got == "https://"+phoneName) {
				t.Fatalf("phone field = %q", got)
			}
			readOnly(t, log)
		})
	}
}

func TestDoctorPhoneRowIsInfoWhenTailscaleIsNotInstalledOrTheBoardIsOff(t *testing.T) {
	fx := newDoctorFixture(t)
	if row := checkRows(t, fx.h.ok("doctor"))["phone"]; row[0] != "info" || !strings.Contains(row[1], "no tailscale answered") {
		t.Fatalf("phone = %q", row)
	}
	fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"))
	installTailscale(t, fx.tools, map[string]string{"serve": "{}"})
	if row := checkRows(t, fx.h.ok("doctor"))["phone"]; row[0] != "info" || !strings.Contains(row[1], "the board is not running") {
		t.Fatalf("phone = %q", row)
	}
}
