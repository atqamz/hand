package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesAStarterRoutingPolicy(t *testing.T) {
	h := newHarness(t)
	if out := h.ok("init"); !strings.Contains(out, "routing: "+filepath.Join(h.home, "routing.json")) {
		t.Fatalf("init = %q", out)
	}
	list := h.ok("route", "list")
	for _, want := range []string{"profiles[3]{name,harness,model,effort,valid}:", "default,claude,sonnet,medium,yes", "deep,claude,opus,xhigh,yes", "harnesses: claude codex opencode"} {
		if !strings.Contains(list, want) {
			t.Fatalf("route list = %q, missing %q", list, want)
		}
	}
}

func TestAttemptStartWithAProfile(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.ok("attempt", "start", "--profile", "deep", "--prompt-file", fx.brief, "t1")
	call := fx.rt.lastCreate()
	if !strings.Contains(strings.Join(call.Command, " "), "--model opus --effort xhigh") {
		t.Fatalf("argv = %q", call.Command)
	}
	if _, errOut, code := fx.h.run("attempt", "start", "--profile", "deep", "--model", "haiku", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "either --profile or --harness") {
		t.Fatalf("profile plus model: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := fx.h.run("attempt", "start", "--profile", "fast", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "unknown profile") {
		t.Fatalf("unknown profile: code=%d stderr=%q", code, errOut)
	}
	if err := os.WriteFile(filepath.Join(fx.h.home, "routing.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := fx.h.run("attempt", "start", "--profile", "deep", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "routing.json") {
		t.Fatalf("malformed policy: code=%d stderr=%q", code, errOut)
	}
}
