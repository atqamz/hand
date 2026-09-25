package herdr

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPowerShellQuoteDoublesSingleQuoteClassCharacters(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"ascii apostrophe", "o'neil", "'o''neil'"},
		{"left single quotation mark U+2018", "o‘neil", "'o‘‘neil'"},
		{"right single quotation mark U+2019", "o’neil", "'o’’neil'"},
		{"single low-9 quotation mark U+201A", "o‚neil", "'o‚‚neil'"},
		{"single high-reversed-9 quotation mark U+201B", "o‛neil", "'o‛‛neil'"},
		{"mixed single-quote-class characters", "o'’‘neil", "'o''’’‘‘neil'"},
		{"fleet home path with curly apostrophe", "/home/o’neil", "'/home/o’’neil'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := powerShellQuote(tc.input)
			if got != tc.want {
				t.Fatalf("powerShellQuote(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPowerShellQuoteRoundTripsThroughPwsh(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not installed; skipping PowerShell round-trip test")
	}
	cases := []string{
		"o'neil",
		"o’neil",
		"o‘’‚‛neil",
	}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			script := "Write-Output " + powerShellQuote(value)
			cmd := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-Command", script)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("pwsh failed: %v, output: %s", err, out)
			}
			got := strings.TrimRight(string(out), "\r\n")
			if got != value {
				t.Fatalf("round trip through pwsh = %q, want %q", got, value)
			}
		})
	}
}
