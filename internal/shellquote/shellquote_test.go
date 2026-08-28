package shellquote

import (
	"os/exec"
	"runtime"
	"testing"

	"pgregory.net/rapid"
)

func TestQuoteRoundTripsPOSIXShellArguments(t *testing.T) {
	for _, value := range []string{
		"fleet home",
		"fleet's home",
		"fleet`printf injected`home",
		"fleet;printf injected",
	} {
		t.Run(value, func(t *testing.T) {
			got, err := exec.Command("sh", "-c", "printf %s "+Quote(value)).CombinedOutput()
			if err != nil {
				t.Fatalf("execute quoted argument: %v: %s", err, got)
			}
			if string(got) != value {
				t.Fatalf("shell produced %q, want the one original argument %q", got, value)
			}
		})
	}
}

// INV-PURE-1: for any string a real shell parses Quote(s) back to exactly s. A
// literal NUL is excluded because no POSIX argv can carry one regardless of
// quoting - that is an argv limit, not a claim about Quote.
func TestQuoteRoundTripsAnyStringThroughARealShell(t *testing.T) {
	// Windows resolves an MSYS-family sh that CRLF-translates its own input,
	// so a lone CR cannot survive regardless of quoting - the claim is about
	// POSIX shells, and a shell that rewrites its input cannot witness it.
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell to witness the claim: the resolved sh CRLF-translates its own input")
	}

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell available to round-trip through: " + err.Error())
	}

	noNUL := rapid.Rune().Filter(func(r rune) bool { return r != 0 })
	body := rapid.StringOfN(noNUL, -1, 300, -1)

	// Quote's only non-literal behavior is around an embedded ' - a single
	// rune among ~140 defaults, so most random draws never exercise it. Half
	// the cases splice one in so the fixed case budget favors what matters.
	rapid.Check(t, func(t *rapid.T) {
		value := body.Draw(t, "value")
		if rapid.Bool().Draw(t, "spliceQuote") {
			runes := []rune(value)
			at := rapid.IntRange(0, len(runes)).Draw(t, "quoteAt")
			value = string(runes[:at]) + "'" + string(runes[at:])
		}

		got, err := exec.Command(sh, "-c", "printf %s "+Quote(value)).CombinedOutput()
		if err != nil {
			t.Fatalf("execute quoted argument: %v: %s", err, got)
		}
		if string(got) != value {
			t.Fatalf("shell produced %q, want the one original argument %q", got, value)
		}
	})
}
