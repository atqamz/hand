package shellquote

import (
	"os/exec"
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
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell available to round-trip through: " + err.Error())
	}

	noNUL := rapid.Rune().Filter(func(r rune) bool { return r != 0 })
	rapid.Check(t, func(t *rapid.T) {
		value := rapid.StringOfN(noNUL, -1, 300, -1).Draw(t, "value")

		got, err := exec.Command(sh, "-c", "printf %s "+Quote(value)).CombinedOutput()
		if err != nil {
			t.Fatalf("execute quoted argument: %v: %s", err, got)
		}
		if string(got) != value {
			t.Fatalf("shell produced %q, want the one original argument %q", got, value)
		}
	})
}
