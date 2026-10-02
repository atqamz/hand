package cli_test

import (
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestHostGetenvFallsBackToUserprofile(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", `C:\Users\op`)
	if got := cli.HostGetenv("HOME"); got != `C:\Users\op` {
		t.Fatalf("HOME = %q", got)
	}
	t.Setenv("HOME", `D:\h`)
	if got := cli.HostGetenv("HOME"); got != `D:\h` {
		t.Fatalf("HOME = %q", got)
	}
}
