package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runBoard(t *testing.T, h *harness, addr ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	a := "127.0.0.1:0"
	if len(addr) > 0 {
		a = addr[0]
	}
	out, errOut, code := h.runCtx(ctx, "board", "--addr", a)
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

func TestBoardFixesTokenPermissionsAndRefusesAMalformedToken(t *testing.T) {
	h := initWithProject(t)
	path := filepath.Join(h.home, "board.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 48)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runBoard(t, h); !strings.Contains(out, "?token="+strings.Repeat("a", 48)) {
		t.Fatalf("existing token not reused: %q", out)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, want 0600", info.Mode().Perm())
	}
	for _, bad := range []string{"short", strings.Repeat("#", 48)} {
		if err := os.WriteFile(path, []byte(bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, errOut, code := h.run("board", "--addr", "127.0.0.1:0"); code != 2 || !strings.Contains(errOut, "not a board token") {
			t.Fatalf("malformed token %q: code=%d stderr=%q", bad, code, errOut)
		}
	}
}

func TestBoardWarnsOnANetworkAddress(t *testing.T) {
	h := initWithProject(t)
	if out := runBoard(t, h, "0.0.0.0:0"); !strings.Contains(out, "plain HTTP") {
		t.Fatalf("no warning for a network address: %q", out)
	}
	if out := runBoard(t, h); strings.Contains(out, "plain HTTP") {
		t.Fatalf("loopback board warns: %q", out)
	}
}

func TestConcurrentBoardsShareOneToken(t *testing.T) {
	h := initWithProject(t)
	type result struct {
		out  string
		code int
	}
	outs := make(chan result, 4)
	for range 4 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			out, _, code := h.runCtx(ctx, "board", "--addr", "127.0.0.1:0")
			outs <- result{out, code}
		}()
	}
	first := ""
	for range 4 {
		r := <-outs
		if r.code != 0 {
			t.Fatalf("a concurrent board exited %d", r.code)
		}
		_, after, _ := strings.Cut(r.out, "?token=")
		tok, _, _ := strings.Cut(after, `"`)
		if first == "" {
			first = tok
		}
		if tok != first || len(tok) != 48 {
			t.Fatalf("concurrent boards printed %q and %q", first, tok)
		}
	}
}
