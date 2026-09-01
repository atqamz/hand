package store

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var legacyV072Checks = map[string][]string{
	"task": {
		"check(lifecyclein('open','terminal'))",
	},
	"attempt": {
		"check(lifecyclein('provisioning','running','completed','failed','interrupted'))",
	},
	"fleet_identity": {
		"check(singleton=1)",
	},
	"send_attempt": {
		"check(originin('operator','usage-limit-resume','legacy-undelivered'))",
		"check(statein('pending','not-submitted','submitted','uncertain'))",
	},
}

var legacyV072AutoIncrement = map[string]bool{
	"attempt":      true,
	"send_attempt": true,
}

// Legacy cutover accepts only the shipped v0.7.2 table semantics that PRAGMA
// layout inspection cannot observe. Semantic DDL drift fails closed even when
// columns, foreign keys, and indexes otherwise match.
func validateLegacyV18DDLFeatures(sqlDB *sql.DB) error {
	rows, err := sqlDB.Query(`SELECT name, sql FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return fmt.Errorf("read legacy v18 table DDL: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			return fmt.Errorf("read legacy v18 table DDL: %w", err)
		}
		compact := compactLegacyV18DDL(ddl)

		checks := legacyV072Checks[name]
		if got := strings.Count(compact, "check("); got != len(checks) {
			return fmt.Errorf("%w: table %s has %d CHECK constraints, want %d", ErrUnsupportedLegacyV18Schema, name, got, len(checks))
		}
		for _, check := range checks {
			if !strings.Contains(compact, check) {
				return fmt.Errorf("%w: table %s is missing exact CHECK constraint %s", ErrUnsupportedLegacyV18Schema, name, check)
			}
		}

		if got, want := strings.Contains(compact, "autoincrement"), legacyV072AutoIncrement[name]; got != want {
			return fmt.Errorf("%w: table %s AUTOINCREMENT = %t, want %t", ErrUnsupportedLegacyV18Schema, name, got, want)
		}

		for _, forbidden := range []string{
			"withoutrowid",
			")strict",
			"collate",
			"onconflict",
			"deferrable",
			"initiallydeferred",
			"generatedalways",
			"primarykeydesc",
		} {
			if strings.Contains(compact, forbidden) {
				return fmt.Errorf("%w: table %s contains unsupported DDL feature %q", ErrUnsupportedLegacyV18Schema, name, forbidden)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read legacy v18 table DDL: %w", err)
	}
	return nil
}

func compactLegacyV18DDL(ddl string) string {
	var compact strings.Builder
	for i := 0; i < len(ddl); {
		switch {
		case strings.HasPrefix(ddl[i:], "--"):
			i += 2
			for i < len(ddl) && ddl[i] != '\n' {
				i++
			}
		case strings.HasPrefix(ddl[i:], "/*"):
			i += 2
			for i < len(ddl) && !strings.HasPrefix(ddl[i:], "*/") {
				i++
			}
			if i < len(ddl) {
				i += 2
			}
		case ddl[i] == '\'':
			compact.WriteByte(ddl[i])
			i++
			for i < len(ddl) {
				compact.WriteByte(ddl[i])
				if ddl[i] == '\'' {
					if i+1 < len(ddl) && ddl[i+1] == '\'' {
						compact.WriteByte(ddl[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case ddl[i] == '"' || ddl[i] == '`':
			quote := ddl[i]
			i++
			for i < len(ddl) {
				if ddl[i] == quote {
					if i+1 < len(ddl) && ddl[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			compact.WriteString("quotedidentifier")
		case ddl[i] == '[':
			i++
			for i < len(ddl) && ddl[i] != ']' {
				i++
			}
			if i < len(ddl) {
				i++
			}
			compact.WriteString("quotedidentifier")
		default:
			r, size := utf8.DecodeRuneInString(ddl[i:])
			i += size
			if unicode.IsSpace(r) {
				continue
			}
			compact.WriteRune(unicode.ToLower(r))
		}
	}
	return compact.String()
}
