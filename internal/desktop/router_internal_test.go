package desktop

import "testing"

func TestAPairedHostsRefusalsShowTheirOwnSentenceNotTheLoopbackOne(t *testing.T) {
	for _, code := range []string{CodeRevoked, CodeNotRecognised, CodeKeyChanged, "forbidden"} {
		if got := friendly(&APIError{Status: 403, Code: code, Remote: true, Message: "said " + code}); got != "said "+code {
			t.Fatalf("%s = %q", code, got)
		}
	}
	for _, code := range []string{"forbidden", ""} {
		if got := friendly(&APIError{Status: 403, Code: code, Message: "x"}); got != "the board's API answers only on its loopback address" {
			t.Fatalf("a local 403 %q = %q", code, got)
		}
	}
}
