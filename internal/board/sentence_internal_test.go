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
