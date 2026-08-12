package cmd

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "HAND_ROLE", value: ""},
		{name: "HAND_HOME", value: ""},
		{name: "HAND_HARNESS", value: "unknown"},
	} {
		if err := os.Setenv(tc.name, tc.value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestCommandPackageStartsWithNeutralEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "HAND_ROLE", want: ""},
		{name: "HAND_HOME", want: ""},
		{name: "HAND_HARNESS", want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := os.Getenv(tc.name); got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
