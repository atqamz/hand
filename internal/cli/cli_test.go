package cli_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestVersionPrintsTOON(t *testing.T) {
	h := newHarness(t)
	if got, want := h.ok("version"), "version: 0.8.0\nchannel: source\ncommit: unknown\nschema: 7\nluvus: 0.14.3\n"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestVersionNamesItsBuild(t *testing.T) {
	version, channel, commit := cli.Version, cli.Channel, cli.Commit
	t.Cleanup(func() { cli.Version, cli.Channel, cli.Commit = version, channel, commit })
	cli.Version, cli.Channel, cli.Commit = "0.9.0", "edge", "0123456789abcdef0123"
	if got, want := newHarness(t).ok("version"), "version: 0.9.0\nchannel: edge\ncommit: 0123456789ab\nschema: 7\nluvus: 0.14.3\n"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	h := newHarness(t)
	_, errOut, code := h.run("frobnicate")
	if code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestNoCommandIsUsageError(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run(); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
