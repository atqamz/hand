package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/toon"
)

func TestInitWritesAnEmptyRoutingPolicy(t *testing.T) {
	h := newHarness(t)
	if out := h.ok("init"); !strings.Contains(out, "routing: "+toon.Value(filepath.Join(h.home, "routing.json"))) {
		t.Fatalf("init = %q", out)
	}
	if b, err := os.ReadFile(filepath.Join(h.home, "routing.json")); err != nil || string(b) != "{}\n" {
		t.Fatalf("routing.json = %q, %v", b, err)
	}
}

func TestAttemptStartTakesTheHarnessFlags(t *testing.T) {
	fx := newAttemptFixture(t)
	if _, errOut, code := fx.h.run("attempt", "start", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "give --harness, --model and --effort") {
		t.Fatalf("no flags: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := fx.h.run("attempt", "start", "--harness", "claude", "--prompt-file", fx.brief, "t1"); code == 0 || !strings.Contains(errOut, "claude") {
		t.Fatalf("claude alone: code=%d stderr=%q", code, errOut)
	}
	if _, errOut, code := fx.h.run("attempt", "start", "--profile", "deep", "--prompt-file", fx.brief, "t1"); code != 2 || !strings.Contains(errOut, "-profile") {
		t.Fatalf("--profile: code=%d stderr=%q", code, errOut)
	}
	fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "xhigh", "--prompt-file", fx.brief, "t1")
	if call := fx.rt.lastCreate(); !strings.Contains(strings.Join(call.Command, " "), "--model sonnet --effort xhigh") {
		t.Fatalf("argv = %q", call.Command)
	}
}

func TestAttemptStartStartsAnOpencodeWithNothingElse(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.h.ok("attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
}

func TestRouteIsAnUnknownCommand(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	if _, _, code := h.run("route", "list"); code != 2 {
		t.Fatalf("route list code = %d", code)
	}
}

func TestAttemptTrackEmptyTrackRecord(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	if list := h.ok("attempt", "track"); !strings.Contains(list, "track[0]{harness,model,effort,n,first_try,reattempt,stuck,sent_per,wakes_per,median_min}:") {
		t.Fatalf("attempt track = %q", list)
	}
}

func TestAttemptTrackShowsTrackRecord(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	list := fx.h.ok("attempt", "track")
	for _, want := range []string{"track[1]{harness,model,effort,n,first_try,reattempt,stuck,sent_per,wakes_per,median_min}:", `claude,sonnet,low,1,0.00,0.00,0.00,0.00,0.00,""`} {
		if !strings.Contains(list, want) {
			t.Fatalf("attempt track = %q, missing %q", list, want)
		}
	}
}
