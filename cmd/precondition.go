package cmd

import (
	"errors"

	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

// These package errors are precondition failures rather than general errors. The imported packages
// cannot construct cmd.ExitError themselves, so they signal through sentinels.
var preconditionSentinels = []error{
	state.ErrTaskNotFound,
	state.ErrTaskActive,
	state.ErrLifecycleConflict,
	state.ErrOwnershipConflict,
	state.ErrHoldNotFound,
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
