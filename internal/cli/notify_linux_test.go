package cli_test

import (
	"slices"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestNotifyArgvUsesNotifySend(t *testing.T) {
	bin, args := cli.NotifyArgv("fleet", "a1 blocked")
	if bin != "notify-send" || !slices.Equal(args, []string{"--app-name=hand", "--", "fleet", "a1 blocked"}) {
		t.Fatalf("notify = %q %q", bin, args)
	}
}

func TestNotifyClickArgvAddsTheAction(t *testing.T) {
	args := cli.ClickArgv("fleet", "a1 blocked")
	want := []string{"--app-name=hand", "--action=default=Open", "--wait", "--", "fleet", "a1 blocked"}
	if !slices.Equal(args, want) {
		t.Fatalf("click notify = %q", args)
	}
}
