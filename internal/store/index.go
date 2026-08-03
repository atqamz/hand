package store

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A separate database file, not a table in hand.db, so that "derived" is a
// property of the storage and not a promise: deleting it costs a rebuild and
// cannot cost machine state or a line of the corpus.
func IndexPath(homeDir string) string {
	return filepath.Join(Dir(homeDir), "index.db")
}

// The prose the index is derived from. These files stay authoritative;
// nothing in hand reads the index to recover them.
func CorpusDir(homeDir string) string {
	return filepath.Join(homeDir, "data")
}

const indexSchema = `
CREATE TABLE IF NOT EXISTS doc (
	path  TEXT PRIMARY KEY,
	size  INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	title TEXT NOT NULL
);
CREATE VIRTUAL TABLE IF NOT EXISTS doc_fts USING fts5(
	path UNINDEXED, title, body, tokenize = 'porter unicode61'
);
`

type Index struct {
	sql  *sql.DB
	home string
}

type Hit struct {
	Path    string
	Title   string
	Snippet string
}

// Reported back so a rebuild can say whether it found anything.
type Sync struct {
	Indexed int
	Removed int
	Total   int
}

func OpenIndex(homeDir string) (*Index, error) {
	sqlDB, err := open(IndexPath(homeDir))
	if err != nil {
		return nil, err
	}
	if _, err := sqlDB.Exec(indexSchema); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("create index schema: %w", err)
	}
	return &Index{sql: sqlDB, home: homeDir}, nil
}

func (ix *Index) Close() error {
	return ix.sql.Close()
}

// Discarding the database and deriving a new one from the corpus is the whole
// recovery story for a corrupt or stale index.
func Rebuild(homeDir string) (Sync, error) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(IndexPath(homeDir) + suffix); err != nil && !os.IsNotExist(err) {
			return Sync{}, fmt.Errorf("remove index: %w", err)
		}
	}
	ix, err := OpenIndex(homeDir)
	if err != nil {
		return Sync{}, err
	}
	defer func() { _ = ix.Close() }()
	return ix.Refresh()
}

type corpusFile struct {
	path  string
	size  int64
	mtime int64
}

// hand search calls this on every query, so a query can never answer from an
// index the corpus has left behind.
func (ix *Index) Refresh() (Sync, error) {
	found, err := scanCorpus(ix.home)
	if err != nil {
		return Sync{}, err
	}

	known := map[string]corpusFile{}
	rows, err := ix.sql.Query(`SELECT path, size, mtime FROM doc`)
	if err != nil {
		return Sync{}, fmt.Errorf("read index: %w", err)
	}
	for rows.Next() {
		var f corpusFile
		if err := rows.Scan(&f.path, &f.size, &f.mtime); err != nil {
			_ = rows.Close()
			return Sync{}, fmt.Errorf("read index: %w", err)
		}
		known[f.path] = f
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Sync{}, fmt.Errorf("read index: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Sync{}, fmt.Errorf("read index: %w", err)
	}

	sync := Sync{Total: len(found)}
	for _, f := range found {
		if was, ok := known[f.path]; ok && was.size == f.size && was.mtime == f.mtime {
			continue
		}
		if err := ix.indexFile(f); err != nil {
			return Sync{}, err
		}
		sync.Indexed++
	}
	for path := range known {
		if _, ok := found[path]; ok {
			continue
		}
		if err := ix.deleteDoc(path); err != nil {
			return Sync{}, err
		}
		sync.Removed++
	}
	return sync, nil
}

func (ix *Index) indexFile(f corpusFile) error {
	body, err := os.ReadFile(filepath.Join(ix.home, f.path))
	if err != nil {
		return fmt.Errorf("read corpus file %s: %w", f.path, err)
	}
	title := docTitle(body, f.path)

	// One transaction, because a doc row records the size and mtime that let
	// Refresh skip the file: landing it without its text would leave the file
	// silently unsearchable until someone thought to rebuild.
	tx, err := ix.sql.Begin()
	if err != nil {
		return fmt.Errorf("index %s: %w", f.path, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM doc WHERE path = ?`, []any{f.path}},
		{`DELETE FROM doc_fts WHERE path = ?`, []any{f.path}},
		{`INSERT INTO doc (path, size, mtime, title) VALUES (?, ?, ?, ?)`, []any{f.path, f.size, f.mtime, title}},
		{`INSERT INTO doc_fts (path, title, body) VALUES (?, ?, ?)`, []any{f.path, title, string(body)}},
	} {
		if _, err := tx.Exec(stmt.query, stmt.args...); err != nil {
			return fmt.Errorf("index %s: %w", f.path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index %s: %w", f.path, err)
	}
	return nil
}

func (ix *Index) deleteDoc(path string) error {
	tx, err := ix.sql.Begin()
	if err != nil {
		return fmt.Errorf("drop %s from index: %w", path, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, query := range []string{`DELETE FROM doc WHERE path = ?`, `DELETE FROM doc_fts WHERE path = ?`} {
		if _, err := tx.Exec(query, path); err != nil {
			return fmt.Errorf("drop %s from index: %w", path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("drop %s from index: %w", path, err)
	}
	return nil
}

// A raw string reaching MATCH exposes FTS5's query syntax, where fleet
// vocabulary is not ordinary text: `no-mistakes gate` parses as a column filter
// and fails. Quoting every token makes the query the literal words.
func matchExpression(query string) string {
	fields := strings.Fields(query)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

func (ix *Index) Search(query string, limit int) ([]Hit, error) {
	match := matchExpression(query)
	if match == "" {
		return nil, nil
	}
	rows, err := ix.sql.Query(`SELECT doc.path, doc.title, snippet(doc_fts, 2, '', '', ' ... ', 14)
		FROM doc_fts JOIN doc ON doc.path = doc_fts.path
		WHERE doc_fts MATCH ? ORDER BY bm25(doc_fts) LIMIT ?`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	defer func() { _ = rows.Close() }()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Title, &h.Snippet); err != nil {
			return nil, fmt.Errorf("search %q: %w", query, err)
		}
		h.Snippet = strings.Join(strings.Fields(h.Snippet), " ")
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	return hits, nil
}

// Nothing renders data/dashboard.md any more (atqamz/secondhand#62), but a
// home initialized before its deletion keeps the last render on disk forever,
// and indexing that would answer a prose search out of a frozen snapshot of
// removed functionality.
var generatedCorpusFiles = map[string]bool{
	filepath.Join("data", "dashboard.md"): true,
}

func scanCorpus(homeDir string) (map[string]corpusFile, error) {
	found := map[string]corpusFile{}
	root := CorpusDir(homeDir)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(homeDir, path)
		if err != nil {
			return err
		}
		if generatedCorpusFiles[rel] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		found[rel] = corpusFile{path: rel, size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	if os.IsNotExist(err) {
		return found, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan corpus: %w", err)
	}
	return found, nil
}

func docTitle(body []byte, path string) string {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(after)
		}
	}
	return path
}
