package desktop

import "testing"

func TestASkillsResultRowTakesTheToneAndIconOfItsState(t *testing.T) {
	for _, tc := range []struct{ state, tone, icon string }{
		{"added", "pass", "passing"},
		{"removed", "pass", "passing"},
		{"updated", "pass", "passing"},
		{"review", "wait", "waiting"},
		{"pending", "wait", "waiting"},
		{"refused", "fail", "failure"},
		{"conflict", "fail", "failure"},
		{"unknown", "neutral", "idle"},
	} {
		tone := skillResultTone(tc.state)
		if tone != tc.tone {
			t.Errorf("row state %q has tone %q, want %q", tc.state, tone, tc.tone)
		}
		if got := stateIconName(tone); got != tc.icon {
			t.Errorf("row state %q draws the %q icon, want %q", tc.state, got, tc.icon)
		}
	}
}
