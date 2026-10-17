package board

import "testing"

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{
		"not found: task t99": "Task t99 was not found.",
		"stop failed":         "Stop failed.",
		"Already done.":       "Already done.",
		"really?":             "Really?",
		"échec du démarrage":  "Échec du démarrage.",
		"  ":                  "",
	} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlainReadsAsWords(t *testing.T) {
	for in, want := range map[string]string{
		"wake: attempt.reported a1: r1 done; attempt.quiet a1: ; decision.answered d1: 2": "attempt reported a1: r1 done; attempt quiet a1; decision answered d1: 2",
		"a5: ":                           "a5",
		"decision.answered d1":           "decision answered d1",
		"found 12:30 and https://x:80/y": "found 12:30 and https://x:80/y",
	} {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnceLeavesNoDanglingColon(t *testing.T) {
	for _, c := range [][4]string{{"s1", "stopped", "", "s1 stopped"}, {"s1", "failed", "no pane", "s1 failed: no pane"}, {"s1", "failed", "failed twice", "s1 failed twice"}} {
		if got := once(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("once(%q, %q, %q) = %q, want %q", c[0], c[1], c[2], got, c[3])
		}
	}
}
