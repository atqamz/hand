package cli

import (
	"slices"
	"testing"
)

func TestScratchEnvironmentReplacesEveryTempVariable(t *testing.T) {
	got := scratchEnvironment([]string{"A=1", "TMPDIR=/tmp", "TMP=/tmp", "TEMP=/tmp", "GOTMPDIR=/tmp", "CLAUDE_CODE_TMPDIR=/tmp", "B=2"}, "/scratch")
	want := []string{"A=1", "B=2", "TMPDIR=/scratch", "TMP=/scratch", "TEMP=/scratch", "GOTMPDIR=/scratch", "CLAUDE_CODE_TMPDIR=/scratch"}
	if !slices.Equal(got, want) {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}
