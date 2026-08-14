package runtime

import (
	"errors"
	"testing"
)

func TestClassifiedErrorsPreserveCause(t *testing.T) {
	cause := errors.New("gate is not initialized")

	for _, test := range []struct {
		name string
		wrap func(error) error
		want ErrorKind
	}{
		{name: "usage", wrap: Usage, want: ErrorUsage},
		{name: "precondition", wrap: Precondition, want: ErrorPrecondition},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.wrap(cause)
			var classified *Error
			if !errors.As(err, &classified) {
				t.Fatalf("errors.As(%v) = false", err)
			}
			if classified.Kind != test.want {
				t.Fatalf("kind = %q, want %q", classified.Kind, test.want)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("errors.Is(%v, cause) = false", err)
			}
		})
	}
}

func TestResultCarriesWarningsWithoutPresentationDependencies(t *testing.T) {
	result := Result{
		ID:       "task-1",
		Project:  "hand",
		Kind:     "ship",
		Worktree: "/tmp/worktree",
		Warnings: []string{"warning: cleanup was incomplete"},
		Help:     []string{"Run `hand status task-1`"},
	}

	if result.Warnings[0] != "warning: cleanup was incomplete" || result.Help[0] == "" {
		t.Fatalf("result = %+v, want structured warning and help", result)
	}
}
