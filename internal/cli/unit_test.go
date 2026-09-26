package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnitFilesRunTheWatcherAndTheBoard(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(t.TempDir(), "my fleet")
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("init")
	exe, _ := os.Executable()
	watch := h.ok("unit", "watch")
	for _, want := range []string{"[Service]", `Environment="HAND_HOME=` + h.home + `"`, `ExecStart="` + exe + `" watch`, "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(watch, want) {
			t.Fatalf("watch unit missing %q:\n%s", want, watch)
		}
	}
	if board := h.ok("unit", "board"); !strings.Contains(board, `ExecStart="`+exe+`" board --addr 127.0.0.1:7777`) {
		t.Fatalf("board unit:\n%s", board)
	}
	if _, _, code := h.run("unit", "luvus"); code != 2 {
		t.Fatalf("unknown unit code = %d, want 2", code)
	}
}
