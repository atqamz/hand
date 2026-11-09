package buildstamp

import "testing"

func TestKnownRejectsTheUnknownStamp(t *testing.T) {
	for id, want := range map[string]bool{"": false, Unknown: false, "0123456789ab": true, "0": true} {
		if got := Known(id); got != want {
			t.Fatalf("Known(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestCommitShortensAKnownRevisionAndNamesTheMissingOne(t *testing.T) {
	for in, want := range map[string]string{
		"0123456789abcdef0123": "0123456789ab",
		"0123456789ab":         "0123456789ab",
		"0123456789abc":        "0123456789ab",
	} {
		if got := Commit(in); got != want {
			t.Fatalf("Commit(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Commit(Unknown); got != Unknown || Known(got) {
		t.Fatalf("Commit(%q) = %q", Unknown, got)
	}
}
