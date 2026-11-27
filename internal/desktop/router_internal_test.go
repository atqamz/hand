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

func TestAnUnacceptedTokenNamesBothCommands(t *testing.T) {
	want := "the board did not accept this fleet's token; stop the board with `hand board stop`, then start it again with `hand board start`"
	if got := friendly(&APIError{Status: 401}); got != want {
		t.Fatalf("401 = %q", got)
	}
}
