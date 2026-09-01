package store

import (
	"errors"
	"fmt"
	"strings"
)

var errLegacyV18CutoverSourceUnsafe = errors.New("legacy v18 cutover source is not mechanically safe")

func validateLegacyV18CutoverSource(q sqliteQueryer) (SchemaInfo, error) {
	info, err := inspectExactLegacyV18Schema(q)
	if err != nil {
		return info, err
	}

	var queryOnly int
	if err := q.QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return info, fmt.Errorf("inspect legacy v18 query_only: %w", err)
	}
	if queryOnly != 1 {
		return info, fmt.Errorf("%w: PRAGMA query_only = %d, want 1", errLegacyV18CutoverSourceUnsafe, queryOnly)
	}

	var foreignKeys int
	if err := q.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return info, fmt.Errorf("inspect legacy v18 foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return info, fmt.Errorf("%w: PRAGMA foreign_keys = %d, want 1", errLegacyV18CutoverSourceUnsafe, foreignKeys)
	}

	var journalMode string
	if err := q.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return info, fmt.Errorf("inspect legacy v18 journal mode: %w", err)
	}
	journalMode = strings.ToLower(strings.TrimSpace(journalMode))
	switch journalMode {
	case "delete", "persist", "truncate":
	default:
		return info, fmt.Errorf("%w: PRAGMA journal_mode = %q is not an eligible rollback-journal mode", errLegacyV18CutoverSourceUnsafe, journalMode)
	}

	var integrity string
	if err := q.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return info, fmt.Errorf("check legacy v18 integrity: %w", err)
	}
	if integrity != "ok" {
		return info, fmt.Errorf("%w: integrity_check = %q, want %q", errLegacyV18CutoverSourceUnsafe, integrity, "ok")
	}

	foreignKeyRows, err := q.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return info, fmt.Errorf("check legacy v18 foreign keys: %w", err)
	}
	hasForeignKeyViolation := foreignKeyRows.Next()
	rowsErr := foreignKeyRows.Err()
	closeErr := foreignKeyRows.Close()
	if rowsErr != nil {
		return info, fmt.Errorf("check legacy v18 foreign keys: %w", rowsErr)
	}
	if closeErr != nil {
		return info, fmt.Errorf("close legacy v18 foreign-key check: %w", closeErr)
	}
	if hasForeignKeyViolation {
		return info, fmt.Errorf("%w: foreign_key_check returned at least one row", errLegacyV18CutoverSourceUnsafe)
	}

	return info, nil
}
