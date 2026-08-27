//go:build test

package store

const sqliteTestPragmas = "&_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)"
