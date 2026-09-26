package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestCommandNameFollowsTheBinary(t *testing.T) {
	for path, want := range map[string]string{"/home/me/.local/bin/hand-next": "hand-next", "/usr/bin/hand": "hand", "/tmp/go-build1/b001/cli.test": "hand", "/x/handy": "hand"} {
		if got := cli.CommandName(path); got != want {
			t.Fatalf("CommandName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestAFleetMadeByHandNextSaysHandNext(t *testing.T) {
	h := newHarness(t)
	h.name = "hand-next"
	out := h.ok("init")
	if !strings.Contains(out, "`hand-next project add") || strings.Contains(out, "`hand ") {
		t.Fatalf("init help = %q", out)
	}
	agents, err := os.ReadFile(filepath.Join(h.home, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agents), "`hand-next orient`") {
		t.Fatalf("AGENTS.md = %q, %v", agents, err)
	}
	h.ok("project", "add", "app", gitRepo(t))
	for i := range 12 {
		h.ok("task", "add", "app", "Task "+strconv.Itoa(i))
	}
	if out := h.ok("orient"); !strings.Contains(out, "run `hand-next task list") || strings.Contains(out, "`hand ") {
		t.Fatalf("orient = %q", out)
	}
	for _, args := range [][]string{{}, {"task"}} {
		if _, errOut, _ := h.run(args...); !strings.Contains(errOut, "usage: hand-next ") {
			t.Fatalf("%q: error = %q", args, errOut)
		}
	}
	h.home = filepath.Join(t.TempDir(), "nope")
	h.cwd = t.TempDir()
	if _, errOut, _ := h.run("task", "list"); !strings.Contains(errOut, "run `hand-next init`") {
		t.Fatalf("error = %q", errOut)
	}
}
