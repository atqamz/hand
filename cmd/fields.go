package cmd

import (
	"fmt"
	"strings"

	"github.com/atqamz/secondhand/internal/axi"
)

// Resolves --fields against cols, defaulting to def. An unknown name is a usage error, not a silently
// narrower schema header.
func pickFields[T any](cols []axi.Column[T], fields, def []string) ([]axi.Column[T], error) {
	want := fields
	if len(want) == 0 {
		want = def
	}
	out, err := axi.Select(cols, want)
	if err != nil {
		return nil, &ExitError{Err: err, Code: 2}
	}
	return out, nil
}

// Keeps --fields honest: it narrows the TOON schema header, and silently ignoring it next to --json
// would hand a caller the full object it asked to narrow.
func rejectFieldsWithJSON(fields []string, asJSON bool) error {
	if len(fields) > 0 && asJSON {
		return &ExitError{Err: fmt.Errorf("--fields applies to the default TOON output, not --json"), Code: 2}
	}
	return nil
}

func fieldsFlagUsage[T any](cols []axi.Column[T], def []string) string {
	return fmt.Sprintf("columns to emit, comma-separated (default %s; any of %s)",
		strings.Join(def, ","), strings.Join(axi.Names(cols), ","))
}
