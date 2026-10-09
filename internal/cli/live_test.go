package cli_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
)

func reporterMain([]string) int {
	env := cli.Env{Name: "hand", Stdin: os.Stdin, Stdout: io.Discard, Stderr: os.Stderr, Getenv: cli.HostGetenv, Environ: os.Environ, Now: time.Now, Getwd: os.Getwd}
	for range 50 {
		if cli.Run([]string{"report", "add", "--status", "done", "--text", "Fixed the login bug."}, env) == 0 {
			return 0
		}
		time.Sleep(200 * time.Millisecond)
	}
	return 1
}

func (h *harness) inheritEnv() {
	for _, kv := range luvus.Scrub(os.Environ()) {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" && k != "XDG_RUNTIME_DIR" && !strings.EqualFold(k, "PATH") && h.vars[k] == "" {
			h.vars[k] = v
		}
	}
}

func TestLiveInitDownloadsTheTestedLuvus(t *testing.T) {
	if os.Getenv("HAND_LUVUS_IT") != "1" {
		t.Skip("set HAND_LUVUS_IT=1 to run against the installed luvus")
	}
	h := newHarness(t)
	h.inheritEnv()
	delete(h.vars, "HAND_LUVUS_BASE")
	noLuvusOnPath(h, t)
	out := h.ok("init")
	if !strings.HasPrefix(field(out, "luvus"), "downloaded and pinned "+luvus.Tested+" (") {
		t.Fatalf("init = %q", out)
	}
	pin, ok, err := luvus.LoadPin(h.vars["SECONDHAND_HOME"])
	if err != nil || !ok {
		t.Fatalf("pin = %+v, %v, %v", pin, ok, err)
	}
	version, err := exec.Command(pin.Path, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "luvus "+luvus.Tested {
		t.Fatalf("%s --version = %q, %v", pin.Path, version, err)
	}
}

func TestLiveAttemptReports(t *testing.T) {
	if os.Getenv("HAND_LUVUS_IT") != "1" {
		t.Skip("set HAND_LUVUS_IT=1 to run against the installed luvus")
	}
	h := newHarness(t)
	h.inheritEnv()
	bin := t.TempDir()
	fakebin.Install(t, bin, "claude", "reporter", nil)
	h.vars["PATH"] = bin + string(os.PathListSeparator) + os.Getenv("PATH")
	h.vars["HOME"] = t.TempDir()
	h.vars["LUVUS_HOME"] = t.TempDir()
	repo, brief := gitRepo(t), filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("Fix the login bug, commit, then stop."), 0o644); err != nil {
		t.Fatal(err)
	}
	luvusBin, err := exec.LookPath("luvus")
	if err != nil {
		t.Fatal(err)
	}
	session := fleet.Session(field(h.ok("init"), "id"))
	t.Cleanup(func() {
		stop := exec.Command(luvusBin, "--session", session, "server", "stop")
		stop.Env = h.env(nil, nil).Environ()
		_ = stop.Run()
	})
	*cli.TrustWait = time.Second
	t.Cleanup(func() { *cli.TrustWait = 15 * time.Second })
	h.ok("project", "add", "app", repo)
	h.ok("task", "add", "app", "Fix login")
	h.ok("task", "start", "t1")
	h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", brief, "t1")
	cursor, deadline := "0", time.Now().Add(90*time.Second)
	for {
		woke := h.ok("wait", "--after", cursor, "--timeout", "10s")
		if strings.Contains(woke, ",attempt.reported,t1,") {
			return
		}
		if cursor = field(woke, "cursor"); time.Now().After(deadline) {
			show, _, _ := h.run("attempt", "show", "a1")
			screen, _, _ := h.run("attempt", "read", "a1")
			t.Fatalf("no attempt.reported within 90s\nshow: %s\nscreen: %s", show, screen)
		}
	}
}
