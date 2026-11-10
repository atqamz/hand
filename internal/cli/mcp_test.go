package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/toon"
)

const mcpPolicy = `{"profiles":{"plain":{"harness":"claude","model":"sonnet","effort":"low"},"browser":{"harness":"claude","model":"sonnet","effort":"low","mcp":"mcp.json"}}}`

func TestAProfileMCPFileStartsTheWorkerWithOnlyThatFile(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, mcpPolicy)
	file := filepath.Join(fx.h.home, "mcp.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.startProfile("browser")
	argv := dropSession(t, fx.rt.lastCreate().Command)
	if !slices.Equal(argv[1:8], []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "--strict-mcp-config", "--mcp-config=" + file}) {
		t.Fatalf("argv = %q", argv)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "mcp: "+toon.Value(file)+"\n") {
		t.Fatalf("show = %q", show)
	}
	if list := fx.h.ok("route", "list"); !strings.Contains(list, "{name,harness,model,effort,valid,warn,access,mcp}") || !strings.Contains(list, "browser,claude,sonnet,low,yes,\"\",open,mcp.json\n") {
		t.Fatalf("route list = %q", list)
	}
}

func TestADefaultProfileShowsNoMCP(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, mcpPolicy)
	fx.startProfile("plain")
	if show := fx.h.ok("attempt", "show", "a1"); strings.Contains(show, "mcp:") {
		t.Fatalf("show = %q", show)
	}
}

func TestAMissingMCPFileFailsTheStartBeforeAnyWorkerRuns(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, mcpPolicy)
	_, errOut, code := fx.h.run("attempt", "start", "--profile", "browser", "--prompt-file", fx.brief, "t1")
	if code != 2 || !strings.Contains(errOut, "mcp.json is not a file") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestMCPIsRefusedForOtherHarnessesOutsideTheFleetHomeAndTheSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	for body, want := range map[string]string{
		`{"profiles":{"x":{"harness":"codex","model":"gpt-6-luna","effort":"low","mcp":"mcp.json"}}}`: `mcp is only available for claude, not codex`,
		`{"profiles":{"x":{"harness":"opencode","mcp":"mcp.json"}}}`:                                  `mcp is only available for claude, not opencode`,
		`{"profiles":{"x":{"harness":"claude","model":"sonnet","effort":"low","mcp":"../mcp.json"}}}`: `must be a path inside the fleet home`,
	} {
		accessPolicy(t, fx, body)
		for _, args := range [][]string{{"route", "list"}, {"attempt", "start", "--profile", "x", "--prompt-file", fx.brief, "t1"}} {
			if _, errOut, code := fx.h.run(args...); code != 2 || !strings.Contains(errOut, want) {
				t.Fatalf("%s %q code=%d stderr=%q", body, args, code, errOut)
			}
		}
	}
	accessPolicy(t, fx, mcpPolicy)
	if _, errOut, code := fx.h.run("supervisor", "start", "--profile", "browser"); code != 2 || !strings.Contains(errOut, "names an MCP file") {
		t.Fatalf("supervisor code=%d stderr=%q", code, errOut)
	}
}

func TestMCPFlagStartsTheWorkerWithOnlyThatFile(t *testing.T) {
	fx := newAttemptFixture(t)
	file := filepath.Join(fx.h.home, "mcp.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--mcp", "mcp.json", "--prompt-file", fx.brief, "t1")
	argv := dropSession(t, fx.rt.lastCreate().Command)
	if !slices.Equal(argv[1:8], []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "low", "--strict-mcp-config", "--mcp-config=" + file}) {
		t.Fatalf("argv = %q", argv)
	}
	if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "mcp: "+toon.Value(file)+"\n") {
		t.Fatalf("show = %q", show)
	}
}

func TestMCPFlagIsRefusedBeforeAnyWorkerRuns(t *testing.T) {
	fx := newAttemptFixture(t)
	if err := os.WriteFile(filepath.Join(fx.h.home, "mcp.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fx.h.home, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	claude := []string{"--harness", "claude", "--model", "sonnet", "--effort", "low"}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"codex":     {[]string{"--harness", "codex", "--model", "gpt-6-luna", "--effort", "low", "--mcp", "mcp.json"}, "mcp is only available for claude, not codex"},
		"opencode":  {[]string{"--harness", "opencode", "--mcp", "mcp.json"}, "mcp is only available for claude, not opencode"},
		"absolute":  {append(slices.Clone(claude), "--mcp", filepath.Join(fx.h.home, "mcp.json")), "must be a path inside the fleet home"},
		"parent":    {append(slices.Clone(claude), "--mcp", "../mcp.json"), "must be a path inside the fleet home"},
		"directory": {append(slices.Clone(claude), "--mcp", "dir"), "dir is not a file"},
		"missing":   {append(slices.Clone(claude), "--mcp", "nope.json"), "nope.json is not a file"},
	} {
		_, errOut, code := fx.h.run(append(append([]string{"attempt", "start"}, c.args...), "--prompt-file", fx.brief, "t1")...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		if len(fx.rt.creates) != 0 {
			t.Fatalf("%s: a pane was created", name)
		}
	}
}

func TestMCPFlagAndAProfileWithMCPAreRefusedTogether(t *testing.T) {
	fx := newAttemptFixture(t)
	accessPolicy(t, fx, mcpPolicy)
	_, errOut, code := fx.h.run("attempt", "start", "--profile", "browser", "--mcp", "mcp.json", "--prompt-file", fx.brief, "t1")
	if code != 2 || !strings.Contains(errOut, "give --mcp or a profile with mcp, not both") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	accessPolicy(t, fx, readonlyPolicy)
	if _, errOut, code := fx.h.run("attempt", "start", "--profile", "look", "--mcp", "mcp.json", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "read-only profile starts without MCP servers") {
		t.Fatalf("readonly code=%d stderr=%q", code, errOut)
	}
}
