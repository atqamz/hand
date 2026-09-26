package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runBoard(t *testing.T, h *harness) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out, errOut, code := h.runCtx(ctx, "board", "--addr", "127.0.0.1:0")
	if code != 0 {
		t.Fatalf("board exit %d: %s", code, errOut)
	}
	return out
}

func TestBoardCommandPrintsAStableLinkAndStops(t *testing.T) {
	h := initWithProject(t)
	first := runBoard(t, h)
	if !strings.Contains(first, `board: "http://127.0.0.1:`) || !strings.Contains(first, "/?token=") {
		t.Fatalf("board out = %q", first)
	}
	info, err := os.Stat(filepath.Join(h.home, "board.token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file = %v, %v", info, err)
	}
	tok := func(out string) string {
		_, after, _ := strings.Cut(out, "?token=")
		v, _, _ := strings.Cut(after, `"`)
		return v
	}
	if a, b := tok(first), tok(runBoard(t, h)); len(a) != 48 || a != b {
		t.Fatalf("tokens %q and %q, want one stable 48-char token", a, b)
	}
}

func TestBoardRejectsABadAddress(t *testing.T) {
	h := initWithProject(t)
	if _, _, code := h.run("board", "--addr", "not-an-address"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
