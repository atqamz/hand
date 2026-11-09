package cli

import (
	"runtime"
	"slices"
	"testing"
)

func TestFleetEnvSilencesOmpOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "inherited value",
			in:   []string{"PATH=/a=b:/c", "PI_NOTIFICATIONS=off", "EMPTY=", "PI_NOTIFICATIONS_OLD=on", "X_PI_NOTIFICATIONS=1"},
			want: []string{"PATH=/a=b:/c", "PI_NOTIFICATIONS=0", "EMPTY=", "PI_NOTIFICATIONS_OLD=on", "X_PI_NOTIFICATIONS=1"},
		},
		{
			name: "absent",
			in:   []string{"PATH=/a=b:/c", "EMPTY=", "PI_NOTIFICATIONS_OLD=on"},
			want: []string{"PATH=/a=b:/c", "EMPTY=", "PI_NOTIFICATIONS_OLD=on", "PI_NOTIFICATIONS=0"},
		},
		{
			name: "duplicates",
			in:   []string{"PI_NOTIFICATIONS=1", "PATH=/bin", "PI_NOTIFICATIONS=2"},
			want: []string{"PI_NOTIFICATIONS=0", "PATH=/bin"},
		},
		{
			name: "empty",
			in:   nil,
			want: []string{"PI_NOTIFICATIONS=0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fleetEnv(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("fleetEnv(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFleetEnvMatchesTheNotificationNameCaseInsensitivelyOnWindows(t *testing.T) {
	got := fleetEnv([]string{"A=1", "pi_notifications=1"})
	want := []string{"A=1", "pi_notifications=1", "PI_NOTIFICATIONS=0"}
	if runtime.GOOS == "windows" {
		want = []string{"A=1", "PI_NOTIFICATIONS=0"}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("fleetEnv = %q, want %q", got, want)
	}
}
