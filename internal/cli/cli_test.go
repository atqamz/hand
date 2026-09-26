package cli_test

import (
	"strings"
	"testing"
)

func TestVersionPrintsTOON(t *testing.T) {
	h := newHarness(t)
	if got, want := h.ok("version"), "version: 0.0.0-next\n"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("frobnicate")
	if code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestNoCommandIsUsageError(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run(); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
