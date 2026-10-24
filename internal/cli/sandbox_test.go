package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/sandbox"
)

func testBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func fieldIs(out, name, value string) bool {
	return strings.Contains(out, name+": "+value) || strings.Contains(out, name+": "+strconv.Quote(value))
}

func TestSandboxPrintShowsThePolicy(t *testing.T) {
	h := newHarness(t)
	home := realPath(t, t.TempDir())
	h.vars["HOME"] = home
	wt := filepath.Join(realPath(t, t.TempDir()), "t1-a2")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	out := h.ok("sandbox", "exec", "--print", "--access", "boxed", "--worktree", wt, "--", testBinary(t))
	want := []string{"access: boxed", "network: open", "rules["}
	if probe := sandbox.Probe(); probe.UnixSockets {
		want = append(want, "backend: "+probe.Backend)
	}
	for _, want := range want {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if !fieldIs(out, "worktree", wt) {
		t.Fatalf("output lacks worktree %q:\n%s", wt, out)
	}
	if strings.Contains(out, ".ssh") {
		t.Fatalf("output grants the ssh folder:\n%s", out)
	}
}

func TestSandboxPrintNamesOpen(t *testing.T) {
	h := newHarness(t)
	out := h.ok("sandbox", "exec", "--print", "--access", "open", "--worktree", realPath(t, t.TempDir()), "--", testBinary(t))
	if !strings.HasPrefix(out, "access: open\n") || !strings.Contains(out, "network: open") {
		t.Fatalf("output = %q", out)
	}
}

func TestSandboxCheckReportsTheBackend(t *testing.T) {
	h := newHarness(t)
	probe := sandbox.Probe()
	out, errOut, code := h.run("sandbox", "check")
	if !strings.Contains(out, "backend: "+probe.Backend) {
		t.Fatalf("output = %q", out)
	}
	if probe.UnixSockets {
		if code != 0 || !strings.Contains(out, "landlock,ok") {
			t.Fatalf("code=%d output = %q", code, out)
		}
		return
	}
	if code == 0 || !strings.Contains(out, "landlock,fail") || !strings.Contains(errOut, "boxed cannot be enforced here") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestSandboxExecRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	wt := t.TempDir()
	if _, errOut, code := h.run("sandbox", "exec", "--access", "jailed", "--worktree", wt, "--", testBinary(t)); code != 2 || !strings.Contains(errOut, "--access must be one of") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--worktree", wt, "--access", "boxed", "--attempt", "a1", "--", "/bin/true"); code != 2 || !strings.Contains(errOut, "not both") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--worktree", wt); code != 2 || !strings.Contains(errOut, "after --") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--worktree", wt, "--"); code != 2 || !strings.Contains(errOut, "give a command after --") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--worktree", wt, "--", testBinary(t)); code != 2 || !strings.Contains(errOut, "--access") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--worktree", wt, testBinary(t), "--access", "boxed"); code != 2 || !strings.Contains(errOut, "after --") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestSandboxExecOpenRunsTheCommand(t *testing.T) {
	h := newHarness(t)
	wt := t.TempDir()
	self := testBinary(t)
	if _, errOut, code := h.run("sandbox", "exec", "--access", "open", "--worktree", wt, "--", self, "one"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(h.exec) != 1 || strings.Join(h.exec[0], " ") != self+" "+self+" one" {
		t.Fatalf("exec = %v", h.exec)
	}
}

func TestSandboxBoxedNeedsASupportedBackend(t *testing.T) {
	h := newHarness(t)
	wt := t.TempDir()
	defer func(prober func() sandbox.Support) { sandbox.Prober = prober }(sandbox.Prober)
	sandbox.Prober = func() sandbox.Support {
		return sandbox.Support{Backend: "landlock", ABI: 8, Reason: "Landlock ABI 8 cannot deny Unix-socket connects"}
	}
	if out := h.ok("sandbox", "exec", "--print", "--access", "boxed", "--worktree", wt, "--", testBinary(t)); !strings.Contains(out, "access: boxed") {
		t.Fatalf("print = %q", out)
	}
	if _, errOut, code := h.run("sandbox", "exec", "--access", "boxed", "--worktree", wt, "--", testBinary(t)); code != 2 || !strings.Contains(errOut, "boxed is unavailable") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if len(h.exec) != 0 {
		t.Fatalf("exec = %v", h.exec)
	}
}
