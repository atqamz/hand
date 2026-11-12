package desktop

import "testing"

func TestToneFollowsTheBoardsSeverityKeys(t *testing.T) {
	want := map[string]string{
		"blocked": "fail", "worker": "fail", "failure": "fail", "resume": "fail",
		"limited": "wait", "nosup": "wait", "stalled": "wait", "decision": "wait",
		"report": "neutral", "quiet": "neutral", "long": "neutral", "kept": "neutral", "artifacts": "neutral",
	}
	for key, tone := range want {
		if got := toneOf(key); got != tone {
			t.Errorf("toneOf(%q) = %q, want %q", key, got, tone)
		}
	}
	p := paletteFor(false)
	if bg, fg := toneColors("failure", p); bg != p.failBG || fg != p.fail {
		t.Errorf("worst=failure is not the fail tone")
	}
}
