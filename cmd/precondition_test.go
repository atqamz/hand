package cmd

import (
	"testing"

	"github.com/atqamz/hand/internal/herdr"
)

func TestAsPreconditionClassifiesEnsureTimeout(t *testing.T) {
	err := asPrecondition(&herdr.SessionEnsureError{Cause: herdr.ErrEnsureTimeout})
	if code := exitCodeFor(t, err); code != 3 {
		t.Fatalf("exit code = %d, want 3 for Ensure timeout (err = %v)", code, err)
	}
}
