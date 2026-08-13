package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func writeTaskAttempt(t *testing.T, home string, task state.Task, attempt state.Attempt) error {
	t.Helper()
	if err := state.CreateTask(home, task); err != nil {
		t.Fatal(err)
	}
	attempt.TaskID = task.ID
	if _, err := state.CreateAttempt(home, attempt); err != nil {
		t.Fatal(err)
	}
	return nil
}

func readTaskAttempt(t *testing.T, home, id string) state.Attempt {
	t.Helper()
	attempt, err := state.ActiveAttempt(home, id)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

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
