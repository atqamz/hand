package cli_test

import (
	"slices"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestNotifyArgvKeepsTheTextOutOfTheScript(t *testing.T) {
	text := `a"; do shell script "x`
	bin, args := cli.NotifyArgv("fleet", text)
	want := []string{"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "fleet", text}
	if bin != "osascript" || !slices.Equal(args, want) {
		t.Fatalf("notify = %q %q", bin, args)
	}
}
