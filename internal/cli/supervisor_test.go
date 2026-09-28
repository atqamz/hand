package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var launchPrompt = regexp.MustCompile("^You are supervisor (s[0-9]+) of the Hand fleet \\S+\\. Follow AGENTS\\.md: run `hand orient` now, then work from the operator's messages and from messages that start with \\[hand v1 wake\\]\\.$")

func newSupervisorFixture(t *testing.T) (*harness, *fakeRuntime) {
	t.Helper()
	h := newHarness(t)
	rt := startRuntime(t, filepath.Join(t.TempDir(), "uhp.sock"))
	h.vars["HAND_LUVUS_SOCKET"] = rt.srv.Socket
	h.vars["PATH"] = fakeBin(t)
	h.vars["HOME"] = t.TempDir()
	h.ok("init")
	return h, rt
}

func has(t *testing.T, what, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("%s = %q, missing %q", what, out, want)
		}
	}
}

func startClaudeSupervisor(h *harness) string {
	h.t.Helper()
	return h.ok("supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low")
}

func TestSupervisorStartLaunchesABackgroundTerminal(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	out := startClaudeSupervisor(h)
	call := rt.lastCreate()
	if call.Label != "hand-supervisor" || call.CWD != h.home {
		t.Fatalf("create = %+v", call)
	}
	argv := call.Command
	if len(argv) != 9 || !strings.HasSuffix(argv[0], "/claude") || !slices.Equal(argv[1:3], []string{"--dangerously-skip-permissions", "--session-id"}) || !slices.Equal(argv[4:8], []string{"--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q", argv)
	}
	if m := launchPrompt.FindStringSubmatch(argv[8]); m == nil || m[1] != "s1" {
		t.Fatalf("prompt = %q", argv[8])
	}
	has(t, "start", out, "supervisor: s1", "harness: claude", "status: running", "pane: 2", "session: "+argv[3])
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running", "session: "+argv[3], "agent: working", "pending: 0")
	if _, errOut, code := h.run("supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low"); code != 3 || !strings.Contains(errOut, "already live") {
		t.Fatalf("second start code=%d stderr=%q", code, errOut)
	}
}

func TestSupervisorStartReusesTheLastSettings(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	if _, errOut, code := h.run("supervisor", "start"); code != 2 || !strings.Contains(errOut, "--harness") {
		t.Fatalf("first bare start code=%d stderr=%q", code, errOut)
	}
	startClaudeSupervisor(h)
	first := rt.lastCreate().Command[3]
	has(t, "stop", h.ok("supervisor", "stop"), "supervisor: s1", "status: stopped")
	has(t, "restart", h.ok("supervisor", "start"), "supervisor: s2", "status: running")
	argv := rt.lastCreate().Command
	if argv[2] != "--session-id" || argv[3] == first || !slices.Equal(argv[4:8], []string{"--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q, first session %s", argv, first)
	}
	if m := launchPrompt.FindStringSubmatch(argv[8]); m == nil || m[1] != "s2" {
		t.Fatalf("prompt = %q", argv[8])
	}
}

func TestSupervisorResumeContinuesTheSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	if _, errOut, code := h.run("supervisor", "resume"); code != 3 || !strings.Contains(errOut, "supervisor start") {
		t.Fatalf("resume with none code=%d stderr=%q", code, errOut)
	}
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	cursor := regexp.MustCompile(`wake_cursor: [0-9]+`).FindString(h.ok("supervisor", "show"))
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "status: running", "session: "+session)
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--dangerously-skip-permissions", "--resume", session, "--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q", argv)
	}
	has(t, "show", h.ok("supervisor", "show"), cursor)
	if _, _, code := h.run("supervisor", "resume"); code != 3 {
		t.Fatalf("resume while live code=%d", code)
	}
}

func TestARestoredSupervisorTerminalIsClosed(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	pid := rt.lastPID()
	rt.srv.SetGeneration("gen-2")
	stray := rt.addShell(t, h.home, "hand-supervisor")
	has(t, "show", h.ok("supervisor", "show"), "status: interrupted", "reason: luvus server restarted")
	if !rt.isClosed(stray) {
		t.Fatal("the restored supervisor terminal was left open")
	}
	if !gone(pid) {
		t.Fatalf("supervisor %d survived the restart", pid)
	}
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "status: running")
	late := rt.addShell(t, h.home, "hand-supervisor")
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "status: running")
	if !rt.isClosed(late) {
		t.Fatal("a stray supervisor terminal next to the live one was left open")
	}
}

func TestSupervisorKeysAreLimited(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	has(t, "keys", h.ok("supervisor", "keys", "--revision", "0", "enter"), "keys: enter")
	if _, errOut, code := h.run("supervisor", "keys", "--revision", "0", "ctrl+c"); code != 2 || !strings.Contains(errOut, "ctrl+c") {
		t.Fatalf("ctrl+c code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter"}) {
		t.Fatalf("keys = %q", got)
	}
}

func TestOpencodeSupervisorPromptIsSubmitted(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	script := "#!/bin/sh\nif [ \"$1\" = session ]; then echo '[]'; exit 0; fi\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(h.vars["PATH"], "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.status, rt.afterKeys = "┃  You are supervisor s1 of the Hand fleet", 7, "idle", "working"
	})
	has(t, "start", h.ok("supervisor", "start", "--harness", "opencode"), "supervisor: s1", "prompt: submitted")
	argv := rt.lastCreate().Command
	if len(argv) != 5 || !slices.Equal(argv[1:4], []string{"--standalone", "--auto", "--prompt"}) || !launchPrompt.MatchString(argv[4]) {
		t.Fatalf("argv = %q", argv)
	}
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter"}) {
		t.Fatalf("keys = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: running", "agent: working")
}
