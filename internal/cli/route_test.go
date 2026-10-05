package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/toon"
)

func TestInitWritesAStarterRoutingPolicy(t *testing.T) {
	h := newHarness(t)
	if out := h.ok("init"); !strings.Contains(out, "routing: "+toon.Value(filepath.Join(h.home, "routing.json"))) {
		t.Fatalf("init = %q", out)
	}
	list := h.ok("route", "list")
	for _, want := range []string{"profiles[5]{name,harness,model,effort,valid}:", "default,claude,sonnet,medium,yes", "quick,claude,sonnet,low,yes", "high,claude,sonnet,high,yes", "deep,claude,sonnet,xhigh,yes", "max,claude,sonnet,max,yes", "harnesses: claude codex opencode"} {
		if !strings.Contains(list, want) {
			t.Fatalf("route list = %q, missing %q", list, want)
		}
	}
}

func TestAttemptStartWithAProfile(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.ok("attempt", "start", "--profile", "deep", "--prompt-file", fx.brief, "t1")
	call := fx.rt.lastCreate()
	if !strings.Contains(strings.Join(call.Command, " "), "--model sonnet --effort xhigh") {
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

func TestRouteListPrintsModels(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	list := h.ok("route", "list")
	for _, want := range []string{"models[", "{harness,model,efforts}", "claude,opus,low medium high xhigh max", "claude,fable,low medium high xhigh max"} {
		if !strings.Contains(list, want) {
			t.Fatalf("route list = %q, missing %q", list, want)
		}
	}
}

func TestRouteListShowsAgyEfforts(t *testing.T) {
	h := newHarness(t)
	h.vars["PATH"] = fakeBin(t)
	h.ok("init")
	list := h.ok("route", "list")
	if !strings.Contains(list, `agy,gemini-3.8-flash-low,""`) || strings.Contains(list, "worker only") {
		t.Fatalf("route list = %q", list)
	}
}

func TestRouteListEmptyTrackRecord(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	if list := h.ok("route", "list"); !strings.Contains(list, "track[0]{harness,model,effort,n,first_try,reattempt,stuck,sent_per,wakes_per,median_min}:") {
		t.Fatalf("route list = %q", list)
	}
}

func TestRouteListShowsTrackRecord(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	list := fx.h.ok("route", "list")
	for _, want := range []string{"track[1]{harness,model,effort,n,first_try,reattempt,stuck,sent_per,wakes_per,median_min}:", `claude,sonnet,low,1,0.00,0.00,0.00,0.00,0.00,""`} {
		if !strings.Contains(list, want) {
			t.Fatalf("route list = %q, missing %q", list, want)
		}
	}
}
