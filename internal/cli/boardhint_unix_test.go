//go:build unix && !darwin

package cli_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestBoardHint(t *testing.T) {
	if !strings.Contains(cli.BoardHint, "systemctl --user start secondhand-board") {
		t.Fatalf("hint = %q", cli.BoardHint)
	}
}
