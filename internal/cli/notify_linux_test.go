package cli_test

import (
	"slices"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestNotifyArgvUsesNotifySend(t *testing.T) {
	bin, args := cli.NotifyArgv("fleet", "a1 blocked")
	if bin != "notify-send" || !slices.Equal(args, []string{"--app-name=hand", "fleet", "a1 blocked"}) {
		t.Fatalf("notify = %q %q", bin, args)
	}
}
