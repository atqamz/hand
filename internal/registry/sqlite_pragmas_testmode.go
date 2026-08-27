//go:build test

package registry

const sqliteTestPragmas = "&_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)"
