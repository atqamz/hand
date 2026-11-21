package desktop

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/inbox"
)

func TestTheWindowReadsTheSameInboxNamesAsTheServer(t *testing.T) {
	const stamp = "20261101-050000-1"
	for _, label := range []string{
		"a.txt", "résumé.pdf", "résumé.pdf", "日本語.txt", "नमस्ते.txt", "𠀀.txt", "_x", ".env", "-x", "a b", "a/b", "a‮b", "a‍b", "🙂", "a\xffb", "a\nb", "",
		strings.Repeat("é", 80), strings.Repeat("é", 81), strings.Repeat("a", 80), strings.Repeat("a", 81),
	} {
		for _, name := range []string{stamp + "-" + label, stamp + ".png", stamp + ".txt", stamp + label} {
			if got, want := inboxName.MatchString(name), inbox.Stored.MatchString(name); got != want {
				t.Errorf("%q: window %v, server %v", name, got, want)
			}
		}
	}
}
