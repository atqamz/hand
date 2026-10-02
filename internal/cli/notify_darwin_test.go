package cli_test

import (
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestNotifyArgvKeepsTheTextOutOfTheScript(t *testing.T) {
	text := `a"; do shell script "x`
	bin, args := cli.NotifyArgv("fleet", text)
	if bin != "osascript" || len(args) < 2 || args[len(args)-2] != "fleet" || args[len(args)-1] != text {
		t.Fatalf("notify = %q %q", bin, args)
	}
	for _, a := range args[:len(args)-2] {
		if a == text {
			t.Fatalf("the text is part of the script: %q", args)
		}
	}
}
