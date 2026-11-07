package cli

import (
	"strings"
	"testing"
)

func TestARequesterWithNoCallerSaysUnknown(t *testing.T) {
	if got := (requester{}).String(); !strings.Contains(got, "unknown") {
		t.Fatalf("requester = %q", got)
	}
}
