package cmd

import (
	"errors"

	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

// preconditionSentinels are errors from internal/state, internal/project, and
// internal/home that SPECS.md classifies as precondition failures (exit code 3)
// rather than general errors (exit code 1). Those packages are imported by cmd,
// so they can't construct ExitError themselves; they signal via these sentinels
// instead. A new sentinel should hold only the trailing phrase and be wrapped as
// fmt.Errorf("<noun> %q <phrase>", name, sentinel), matching ErrTaskNotFound and
// ErrNotFound, so each condition renders one consistent string everywhere.
var preconditionSentinels = []error{
	state.ErrTaskNotFound,
	state.ErrTaskActive,
	project.ErrNotFound,
	home.ErrNotFound,
	home.ErrHandHomeInvalid,
}

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
