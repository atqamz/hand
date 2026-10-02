package cli_test

import (
	"strings"
	"testing"
)

func TestUpdateRefusesASourceBuildWithoutAChannel(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if out, errOut, code := h.run("update"); code != 2 || out != "" || !strings.Contains(errOut, "update: this hand was built from source; pass --channel edge or --channel stable") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestUpdateRefusesAnUnknownChannel(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if _, errOut, code := h.run("update", "--channel", "nightly"); code != 2 || !strings.Contains(errOut, "update: --channel must be edge or stable") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestUpdateTakesNoArguments(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if _, _, code := h.run("update", "--channel", "edge", "now"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
