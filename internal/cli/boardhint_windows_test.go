//go:build windows

package cli_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestBoardHint(t *testing.T) {
	if !strings.Contains(cli.BoardHint, "shell:startup") {
		t.Fatalf("hint = %q", cli.BoardHint)
	}
	if strings.Contains(cli.BoardHint, "systemctl") != (false) {
		t.Fatalf("hint = %q", cli.BoardHint)
	}
}
