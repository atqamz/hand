//go:build !linux

package cmd

import (
	"errors"
	"testing"
)

func TestExecGuardRefusesWhereItCannotProveDescendantCessation(t *testing.T) {
	_, _, err := executeRootForTest(t, devBuild("test"), nil, "exec-guard", "/fleet/state/exec-guard/launch/handoff")
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want an unsupported refusal", err)
	}
}
