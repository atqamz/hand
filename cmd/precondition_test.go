package cmd

import (
	"fmt"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
)

func TestAsPreconditionClassifiesEnsureTimeout(t *testing.T) {
	err := asPrecondition(&herdr.SessionEnsureError{Cause: herdr.ErrEnsureTimeout})
	if code := exitCodeFor(t, err); code != 3 {
		t.Fatalf("exit code = %d, want 3 for Ensure timeout (err = %v)", code, err)
	}
}

// atqamz/hand#460: an ambiguous fleet home is a precondition failure like any other home
// resolution problem (home.ErrNotFound, home.ErrHandHomeInvalid), not a general error.
func TestAsPreconditionClassifiesAmbiguousHome(t *testing.T) {
	err := asPrecondition(fmt.Errorf("resolve fleet home: %w", home.ErrAmbiguousHome))
	if code := exitCodeFor(t, err); code != 3 {
		t.Fatalf("exit code = %d, want 3 for an ambiguous home (err = %v)", code, err)
	}
}
