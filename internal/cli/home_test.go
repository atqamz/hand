package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesHomeAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	first := h.ok("init")
	if !strings.Contains(first, "created: true") {
		t.Fatalf("first init = %q", first)
	}
	if _, err := os.Stat(filepath.Join(h.home, "hand.db")); err != nil {
		t.Fatalf("hand.db: %v", err)
	}
	if second := h.ok("init"); !strings.Contains(second, "created: false") {
		t.Fatalf("second init = %q", second)
	}
}
