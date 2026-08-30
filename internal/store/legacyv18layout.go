package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const legacyV072LayoutFingerprint = "7cf67e6ac1c738e348d91ca552f66daa7b5eaf6e4aab9db84a78b49b70a07bec"

func legacyV18LayoutFingerprint(sqlDB *sql.DB) (string, error) {
	lines, err := legacyV18LayoutLines(sqlDB)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, line := range lines {
		_, _ = hash.Write([]byte(line + "\n"))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func legacyV18LayoutLines(sqlDB *sql.DB) ([]string, error) {
	tables, err := legacyV18TableNames(sqlDB)
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, 128)
	for _, table := range tables {
		lines = append(lines, "table|"+table)
		columns, err := legacyV18ColumnLines(sqlDB, table)
		if err != nil {
			return nil, err
		}
		lines = append(lines, columns...)
		foreignKeys, err := legacyV18ForeignKeyLines(sqlDB, table)
		if err != nil {
			return nil, err
		}
		lines = append(lines, foreignKeys...)
		indexes, err := legacyV18IndexLines(sqlDB, table)
		if err != nil {
			return nil, err
		}
		lines = append(lines, indexes...)
	}

	sort.Strings(lines)
	return lines, nil
}

func legacyV18TableNames(sqlDB *sql.DB) ([]string, error) {
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read legacy v18 tables: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy v18 tables: %w", err)
	}
	return tables, nil
}

func legacyV18ColumnLines(sqlDB *sql.DB, table string) ([]string, error) {
	rows, err := sqlDB.Query(`SELECT name, type, "notnull", dflt_value, pk, hidden
		FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 columns for %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var name, typ string
		var notNull, primaryKey, hidden int
		var defaultValue sql.NullString
		if err := rows.Scan(&name, &typ, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return nil, fmt.Errorf("read legacy v18 columns for %s: %w", table, err)
		}
		// Shipped pre-split homes had hold.kind NOT NULL without a default; fresh v0.7.2
		// has DEFAULT ''. Cutover never writes the source, so those forms are equivalent.
		if table == "hold" && name == "kind" && !defaultValue.Valid {
			defaultValue = sql.NullString{String: "''", Valid: true}
		}
		lines = append(lines, fmt.Sprintf("column|%s|%s|%s|%d|%s|%d|%d",
			table, name, strings.ToUpper(strings.TrimSpace(typ)), notNull,
			legacyV18NullableText(defaultValue), primaryKey, hidden))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy v18 columns for %s: %w", table, err)
	}
	return lines, nil
}

func legacyV18ForeignKeyLines(sqlDB *sql.DB, table string) ([]string, error) {
	rows, err := sqlDB.Query(`SELECT "from", "table", "to", on_update, on_delete, match
		FROM pragma_foreign_key_list(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 foreign keys for %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var from, targetTable, to, onUpdate, onDelete, match string
		if err := rows.Scan(&from, &targetTable, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, fmt.Errorf("read legacy v18 foreign keys for %s: %w", table, err)
		}
		lines = append(lines, fmt.Sprintf("fk|%s|%s|%s|%s|%s|%s|%s",
			table, from, targetTable, to, onUpdate, onDelete, match))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy v18 foreign keys for %s: %w", table, err)
	}
	return lines, nil
}

type legacyV18IndexDescriptor struct {
	Name    string
	Origin  string
	Unique  int
	Partial int
}

func legacyV18IndexLines(sqlDB *sql.DB, table string) ([]string, error) {
	rows, err := sqlDB.Query(`SELECT name, "unique", origin, partial FROM pragma_index_list(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("read legacy v18 indexes for %s: %w", table, err)
	}

	var indexes []legacyV18IndexDescriptor
	for rows.Next() {
		var index legacyV18IndexDescriptor
		if err := rows.Scan(&index.Name, &index.Unique, &index.Origin, &index.Partial); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read legacy v18 indexes for %s: %w", table, err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("read legacy v18 indexes for %s: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close legacy v18 indexes for %s: %w", table, err)
	}

	var lines []string
	for _, index := range indexes {
		keys, err := legacyV18IndexKey(sqlDB, index.Name)
		if err != nil {
			return nil, err
		}
		if index.Origin == "c" {
			predicate, err := legacyV18IndexPredicate(sqlDB, index.Name, index.Partial != 0)
			if err != nil {
				return nil, err
			}
			lines = append(lines, fmt.Sprintf("index|%s|%s|%d|%d|%s|%s",
				table, index.Name, index.Unique, index.Partial, keys, predicate))
			continue
		}
		lines = append(lines, fmt.Sprintf("constraint-index|%s|%s|%d|%d|%s",
			table, index.Origin, index.Unique, index.Partial, keys))
	}
	return lines, nil
}

func legacyV18IndexKey(sqlDB *sql.DB, index string) (string, error) {
	rows, err := sqlDB.Query(`SELECT seqno, name, "desc", coll, key FROM pragma_index_xinfo(?)
		WHERE key = 1 ORDER BY seqno`, index)
	if err != nil {
		return "", fmt.Errorf("read legacy v18 index %s: %w", index, err)
	}
	defer func() { _ = rows.Close() }()

	var parts []string
	for rows.Next() {
		var sequence, descending, key int
		var name, collation sql.NullString
		if err := rows.Scan(&sequence, &name, &descending, &collation, &key); err != nil {
			return "", fmt.Errorf("read legacy v18 index %s: %w", index, err)
		}
		parts = append(parts, fmt.Sprintf("%d:%s:%d:%s",
			sequence, legacyV18NullableText(name), descending, legacyV18NullableText(collation)))
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read legacy v18 index %s: %w", index, err)
	}
	return strings.Join(parts, ","), nil
}

func legacyV18IndexPredicate(sqlDB *sql.DB, index string, partial bool) (string, error) {
	if !partial {
		return "", nil
	}
	var ddl string
	if err := sqlDB.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&ddl); err != nil {
		return "", fmt.Errorf("read legacy v18 partial index %s: %w", index, err)
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(ddl), " "))
	where := strings.Index(normalized, " where ")
	if where < 0 {
		return "", fmt.Errorf("read legacy v18 partial index %s: missing WHERE predicate", index)
	}
	return strings.TrimSpace(normalized[where+len(" where "):]), nil
}

func legacyV18NullableText(value sql.NullString) string {
	if !value.Valid {
		return "<null>"
	}
	return strings.TrimSpace(value.String)
}
