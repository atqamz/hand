package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

func (s *Store) Backup(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("state: backup %s: %w", path, err)
	}
	err := check(ctx, path)
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func check(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+uriPath.Replace(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("state: backup %s: %w", path, err)
	}
	if result != "ok" {
		return fmt.Errorf("state: backup %s failed integrity_check: %s", path, result)
	}
	return nil
}
