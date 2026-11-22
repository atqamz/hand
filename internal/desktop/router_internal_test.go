package desktop

import "testing"

func TestAPairedHostsRefusalsShowTheirOwnSentenceNotTheLoopbackOne(t *testing.T) {
	for _, code := range []string{CodeRevoked, CodeNotRecognised, CodeKeyChanged} {
		if got := friendly(&APIError{Status: 403, Code: code, Message: "said " + code}); got != "said "+code {
			t.Fatalf("%s = %q", code, got)
		}
	}
	if got := friendly(&APIError{Status: 403, Code: "forbidden", Message: "x"}); got != "the board's API answers only on its loopback address" {
		t.Fatalf("a plain 403 = %q", got)
	}
}
