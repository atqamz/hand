package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

type doctorFixture struct {
	h     *harness
	id    string
	tools string
	self  string
	srv   *fakeuhp.Server
}

func newDoctorFixture(t *testing.T) *doctorFixture {
	t.Helper()
	channel := cli.Channel
	t.Cleanup(func() { cli.Channel = channel })
	cli.Channel = "stable"
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fx := &doctorFixture{h: newHarness(t), tools: fakeBin(t), self: filepath.Dir(self)}
	h := fx.h
	fakebin.Install(t, fx.tools, "git", "fake", map[string]string{"on --version": "git version 2.51.0\n", "on config": "true\n"})
	h.vars["PATH"] = fx.tools + string(os.PathListSeparator) + fx.self
	h.vars["HOME"] = t.TempDir()
	h.vars["CODEX_HOME"] = t.TempDir()
	if err := os.WriteFile(filepath.Join(h.vars["CODEX_HOME"], "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pinnable(t, h, "0.14.3")
	h.vars["HAND_LUVUS_SOCKET"] = fakeuhp.ShortSocket(t)
	fx.id = field(h.ok("init"), "id")
	fx.srv = fakeuhp.Start(t, h.vars["HAND_LUVUS_SOCKET"])
	fx.serve("0.14.3")
	fx.board(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+fx.id+"/proof" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("ab", 32))
	})
	if runtime.GOOS == "linux" {
		run := t.TempDir()
		if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		fakebin.Install(t, fx.tools, "systemd-run", "fake", nil)
		h.vars["XDG_RUNTIME_DIR"] = run
	}
	return fx
}

func (fx *doctorFixture) serve(version string) {
	fx.srv.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"type": "pong", "version": version}, nil })
}

func (fx *doctorFixture) board(handler http.HandlerFunc) {
	srv := httptest.NewServer(handler)
	fx.h.t.Cleanup(srv.Close)
	if err := os.WriteFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), []byte(strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o644); err != nil {
		fx.h.t.Fatal(err)
	}
}

func (fx *doctorFixture) remove(path string) {
	if err := os.Remove(path); err != nil {
		fx.h.t.Fatal(err)
	}
}

func (fx *doctorFixture) write(path, body string) {
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		fx.h.t.Fatal(err)
	}
}

func checkRows(t *testing.T, out string) map[string][2]string {
	t.Helper()
	rows := map[string][2]string{}
	_, table, ok := strings.Cut(out, "{check,state,fix}:\n")
	if !ok {
		t.Fatalf("no checks in %q", out)
	}
	for _, l := range strings.Split(table, "\n") {
		cells, ok := strings.CutPrefix(l, "  ")
		if !ok || strings.HasPrefix(cells, "- ") {
			break
		}
		check, rest, _ := strings.Cut(cells, ",")
		level, fix, _ := strings.Cut(rest, ",")
		rows[check] = [2]string{level, fix}
	}
	return rows
}

func TestDoctorPassesOnAHealthyFleet(t *testing.T) {
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	out := fx.h.ok("doctor")
	has(t, "doctor", out, "version: "+cli.Version+"\n", "channel: stable\n", "git: 2.51.0\n", "server: 0.14.3\n")
	if !strings.HasPrefix(field(out, "luvus"), "0.14.3 ") {
		t.Fatalf("luvus = %q", field(out, "luvus"))
	}
	rows := checkRows(t, out)
	want := []string{"hand", "git", "harness claude", "harness codex", "harness opencode", "harness agy", "luvus pin", "schema", "fleet", "routing.json", "profile default", "profile quick", "luvus server", "board", "watcher"}
	if runtime.GOOS == "linux" {
		want = append(want, "systemd")
	}
	for _, check := range want {
		if rows[check] != [2]string{"ok", `""`} {
			t.Fatalf("%s = %q in %q", check, rows[check], out)
		}
	}
	if got := rows["phone"][0]; got != "info" {
		t.Fatalf("phone = %q with no tailscale in %q", rows["phone"], out)
	}
	for _, name := range state.Harnesses {
		if row := rows["models "+name]; row[0] != "info" || !strings.Contains(row[1], "no list yet; run `hand models --refresh "+name+"`") {
			t.Fatalf("models %s = %q in %q", name, row, out)
		}
	}
	if len(rows) != len(want)+8 {
		t.Fatalf("rows = %d, want %d: %q", len(rows), len(want)+8, out)
	}
}

const claudeAnswer = `{"type":"control_response","response":{"subtype":"success","request_id":"hand-models","response":{"models":[{"value":"opus","displayName":"Opus","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},{"value":"acme-opus","displayName":"Acme","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]}],"account":{"email":"INVENTED-SECRET-DO-NOT-LEAK"}}}}` + "\n"

func TestDoctorReportsWhatEachHarnessListed(t *testing.T) {
	fx := newDoctorFixture(t)
	fakebin.Install(t, fx.tools, "claude", "fake", map[string]string{"on --version": "claude 9.9\n", "on -p": claudeAnswer})
	fx.h.ok("models", "--refresh")
	out := fx.h.ok("doctor")
	rows := checkRows(t, out)
	for check, want := range map[string]string{
		"models claude":   "2 detected (built-in 1, custom 1), cache <1m old",
		"models codex":    "1 detected (discovered 1), cache <1m old",
		"models agy":      "1 detected (discovered 1), cache <1m old",
		"models opencode": "no list: opencode models --standalone printed no model names",
	} {
		if row := rows[check]; row[0] != "info" || !strings.Contains(row[1], want) {
			t.Fatalf("%s = %q, want %q in %q", check, row, want, out)
		}
	}
	if strings.Contains(out, "INVENTED-SECRET-DO-NOT-LEAK") {
		t.Fatalf("doctor printed the secret: %q", out)
	}
	fx.h.advance(2 * time.Hour)
	if row := checkRows(t, fx.h.ok("doctor"))["models claude"]; !strings.Contains(row[1], "cache 2h old; stale") {
		t.Fatalf("models claude after 2 hours = %q", row)
	}
}

func TestDoctorNamesTheFixForEachProblem(t *testing.T) {
	for _, c := range []struct {
		name, check, level, fix string
		exit                    int
		break_                  func(fx *doctorFixture)
	}{
		{"hand not on PATH", "hand", "warn", "PATH does not lead to this binary; put ", 0, func(fx *doctorFixture) { fx.h.vars["PATH"] = fx.tools }},
		{"source build", "hand", "info", "--channel edge or --channel stable", 0, func(*doctorFixture) { cli.Channel = "source" }},
		{"no git", "git", "fail", "install git", 1, func(fx *doctorFixture) { fx.remove(exe(fx.tools, "git")) }},
		{"profile with an unverified model", "profile quick", "warn", "claude model acme-opus is not an alias", 0, func(fx *doctorFixture) {
			fx.write(filepath.Join(fx.h.home, "routing.json"), `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"quick":{"harness":"claude","model":"acme-opus","effort":"low"}}}`)
		}},
		{"a profile whose model left the list", "profile quick", "warn", "model gpt-5.5 is no longer listed by codex", 0, func(fx *doctorFixture) {
			fx.write(filepath.Join(fx.h.home, "routing.json"), `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"},"quick":{"harness":"codex","model":"gpt-5.5","effort":"low"}}}`)
			bundled := func(slugs ...string) string {
				var models []string
				for _, s := range slugs {
					models = append(models, `{"slug":"`+s+`","visibility":"list","supported_reasoning_levels":[{"effort":"low"}]}`)
				}
				return `{"models":[` + strings.Join(models, ",") + `]}`
			}
			fakebin.Install(fx.h.t, fx.tools, "codex", "fake", map[string]string{"on --version": "codex 1\n", "on debug": bundled("gpt-5.5", "gpt-6-luna")})
			fx.h.ok("models", "--refresh", "codex")
			fakebin.Install(fx.h.t, fx.tools, "codex", "fake", map[string]string{"on --version": "codex 2\n", "on debug": bundled("gpt-6-luna")})
			fx.h.ok("models", "--refresh", "codex")
		}},
		{"no codex list", "models codex", "info", "no list yet; run `hand models --refresh codex`", 0, func(fx *doctorFixture) {}},
		{"agy signed out", "models agy", "info", "no list: agy is not signed in; run agy and sign in", 0, func(fx *doctorFixture) {
			fakebin.Install(fx.h.t, fx.tools, "agy", "fake", map[string]string{"on --version": "agy 1\n", "exit": "1", "stderr": "Error: Please sign in to view available models."})
			fx.h.ok("models", "--refresh", "agy")
		}},
		{"no systemd user session", "systemd", "info", "detached processes", 0, func(fx *doctorFixture) {
			if runtime.GOOS != "linux" {
				fx.h.t.Skip("systemd is checked on Linux only")
			}
			delete(fx.h.vars, "XDG_RUNTIME_DIR")
		}},
		{"pin changed", "luvus pin", "fail", "changed on disk", 1, func(fx *doctorFixture) {
			pinned := strings.TrimPrefix(field(fx.h.ok("luvus", "show"), "pin"), "0.14.3 ")
			if err := os.Chmod(pinned, 0o755); err != nil {
				fx.h.t.Fatal(err)
			}
			fx.remove(pinned)
			fx.write(pinned, "tampered\n")
		}},
		{"no pin", "luvus pin", "warn", "pin it with `hand luvus pin`", 0, func(fx *doctorFixture) {
			fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "luvus", "pin.json"))
		}},
		{"no luvus at all", "luvus pin", "fail", "no Luvus is pinned or on PATH", 1, func(fx *doctorFixture) {
			fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "luvus", "pin.json"))
			fx.remove(exe(fx.tools, "luvus"))
		}},
		{"broken database", "schema", "fail", "", 1, func(fx *doctorFixture) { fx.write(filepath.Join(fx.h.home, "hand.db"), "not a database") }},
		{"not registered", "fleet", "fail", "run `hand init` here", 1, func(fx *doctorFixture) {
			fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "fleets", fx.id))
		}},
		{"bad routing.json", "routing.json", "fail", "routing.json", 1, func(fx *doctorFixture) { fx.write(filepath.Join(fx.h.home, "routing.json"), "{") }},
		{"no claude", "profile default", "fail", "claude not found on PATH; install claude, or add its folder to PATH", 1, func(fx *doctorFixture) { fx.remove(exe(fx.tools, "claude")) }},
		{"server stale", "luvus server", "warn", "run `hand update`", 0, func(fx *doctorFixture) { fx.serve("0.14.2") }},
		{"server not running", "luvus server", "info", "not running", 0, func(fx *doctorFixture) {
			fx.h.vars["HAND_LUVUS_SOCKET"] = filepath.Join(fx.h.t.TempDir(), "none.sock")
		}},
		{"no board", "board", "warn", "no board is running", 0, func(fx *doctorFixture) {
			fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"))
		}},
		{"another board", "board", "warn", "no board for this fleet answers", 0, func(fx *doctorFixture) { fx.board(http.NotFound) }},
		{"no supervisor", "watcher", "info", "none is needed until a supervisor starts", 0, func(*doctorFixture) {}},
		{"no watcher", "watcher", "warn", "a supervisor runs but no watcher does", 0, func(fx *doctorFixture) {
			st, err := state.Open(filepath.Join(fx.h.home, "hand.db"), time.Now)
			if err != nil {
				fx.h.t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.AddSupervisor(context.Background(), state.SupervisorSpec{Harness: "claude", Argv: []string{"claude"}}); err != nil {
				fx.h.t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newDoctorFixture(t)
			c.break_(fx)
			out, errOut, code := fx.h.run("doctor")
			if row := checkRows(t, out)[c.check]; code != c.exit || row[0] != c.level || !strings.Contains(row[1], c.fix) {
				t.Fatalf("code=%d %s=%q stderr=%q out=%q", code, c.check, row, errOut, out)
			}
			if c.exit != 0 && !strings.Contains(errOut, "check(s) failed; follow their fix lines") {
				t.Fatalf("stderr = %q", errOut)
			}
		})
	}
}

func TestDoctorChecksLongPathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("core.longpaths matters on Windows only")
	}
	fx := newDoctorFixture(t)
	fakebin.Install(t, fx.tools, "git", "fake", map[string]string{"on --version": "git version 2.51.0.windows.1\n", "on config": "false\n"})
	if row := checkRows(t, fx.h.ok("doctor"))["git"]; row[0] != "warn" || !strings.Contains(row[1], "git config --global core.longpaths true") {
		t.Fatalf("git = %q", row)
	}
}

func TestDoctorRepairsNothing(t *testing.T) {
	fx := newDoctorFixture(t)
	link := filepath.Join(fx.h.vars["SECONDHAND_HOME"], "fleets", fx.id)
	fx.remove(link)
	fx.remove(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"))
	if _, _, code := fx.h.run("doctor"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	for _, path := range []string{link, filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), filepath.Join(fx.h.home, "board.token")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s exists after doctor: %v", path, err)
		}
	}
}

func TestDoctorOutsideAFleet(t *testing.T) {
	fx := newDoctorFixture(t)
	fx.h.vars["HAND_HOME"], fx.h.cwd = "", t.TempDir()
	out := fx.h.ok("doctor")
	rows := checkRows(t, out)
	if rows["fleet"][0] != "info" || !strings.Contains(rows["fleet"][1], "not inside a fleet home") || !strings.Contains(rows["fleet"][1], "the fleet checks were skipped") {
		t.Fatalf("fleet = %q", rows["fleet"])
	}
	for _, check := range []string{"schema", "routing.json", "profile default", "luvus server", "board", "watcher"} {
		if _, ok := rows[check]; ok {
			t.Fatalf("%s checked outside a fleet: %q", check, out)
		}
	}
	if rows["luvus pin"][0] != "ok" || rows["git"][0] != "ok" {
		t.Fatalf("rows = %q", rows)
	}
	fx.h.vars["HAND_HOME"] = t.TempDir()
	if out, _, code := fx.h.run("doctor"); code != 1 || !strings.Contains(checkRows(t, out)["fleet"][1], "no hand home at") {
		t.Fatalf("a HAND_HOME without a fleet: code=%d out=%q", code, out)
	}
	fx.h.vars["HAND_HOME"], fx.h.vars["SECONDHAND_HOME"], fx.h.vars["HOME"] = "", "", ""
	if out, _, code := fx.h.run("doctor"); code != 1 || checkRows(t, out)["luvus pin"][0] != "fail" || !strings.Contains(checkRows(t, out)["fleet"][1], "the fleet checks were skipped") {
		t.Fatalf("no secondhand folder: code=%d out=%q", code, out)
	}
	if _, errOut, code := fx.h.run("doctor", "extra"); code != 2 || !strings.Contains(errOut, "doctor: want 0 argument(s)") {
		t.Fatalf("usage: code=%d stderr=%q", code, errOut)
	}
}
