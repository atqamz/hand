package cmd

import (
	"errors"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

// preconditionSentinels are errors from internal/state and internal/project that
// SPECS.md classifies as precondition failures (exit code 3) rather than general
// errors (exit code 1). Both packages are imported by cmd, so they can't construct
// ExitError themselves; they signal via these sentinels instead.
var preconditionSentinels = []error{
	state.ErrTaskNotFound,
	state.ErrTaskActive,
	project.ErrNotFound,
}

// asPrecondition tags err as exit code 3 if it wraps one of preconditionSentinels,
// otherwise returns it unchanged so it defaults to the general exit code 1.
func asPrecondition(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range preconditionSentinels {
		if errors.Is(err, sentinel) {
			return &ExitError{Err: err, Code: 3}
		}
	}
	return err
}
