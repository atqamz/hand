package cmd

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// INV-OUT-6: rejected only when both --fields and --json are requested, for
// any field set - the general form of status_test.go's
// TestStatusFieldsWithJSONIsAUsageError.
func TestRejectFieldsWithJSONRefusesOnlyWhenBothAreRequested(t *testing.T) {
	fieldName := rapid.StringMatching(`[a-z]{1,8}`)

	rapid.Check(t, func(t *rapid.T) {
		fields := rapid.SliceOfN(fieldName, 0, 5).Draw(t, "fields")
		asJSON := rapid.Bool().Draw(t, "json")

		err := rejectFieldsWithJSON(fields, asJSON)
		combined := len(fields) > 0 && asJSON

		if combined && err == nil {
			t.Fatalf("rejectFieldsWithJSON(%v, %v) = nil, want a usage error rejecting --fields with --json", fields, asJSON)
		}
		if !combined && err != nil {
			t.Fatalf("rejectFieldsWithJSON(%v, %v) = %v, want nil: --fields and --json were not both requested", fields, asJSON, err)
		}
		if err == nil {
			return
		}
		var exitErr *ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("rejectFieldsWithJSON(%v, %v) error = %v, want an ExitError with code 2", fields, asJSON, err)
		}
		if !strings.Contains(err.Error(), "--fields") || !strings.Contains(err.Error(), "--json") {
			t.Fatalf("rejectFieldsWithJSON(%v, %v) error = %q, want it to name both flags", fields, asJSON, err.Error())
		}
	})
}
