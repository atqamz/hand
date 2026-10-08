package luvus

import (
	"slices"
	"testing"
)

func TestLongPathsOnlyOnWindowsAndNeverOverAUserSetting(t *testing.T) {
	base := []string{"PATH=x"}
	if got := longPaths("linux", base); !slices.Equal(got, base) {
		t.Fatalf("linux env = %q", got)
	}
	got := longPaths("windows", []string{"PATH=x"})
	for _, want := range []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.longpaths", "GIT_CONFIG_VALUE_0=true"} {
		if !slices.Contains(got, want) {
			t.Fatalf("windows env lacks %s: %q", want, got)
		}
	}
	own := []string{"Git_Config_Count=2"}
	if got := longPaths("windows", own); !slices.Equal(got, own) {
		t.Fatalf("overrode a user's git config: %q", got)
	}
}
