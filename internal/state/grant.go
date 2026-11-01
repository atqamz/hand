package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Grant struct {
	ID        int64
	Project   string
	Harness   string
	Pattern   string
	CreatedAt string
}

func (s *Store) AddGrant(ctx context.Context, project, harness, pattern string) (Grant, bool, error) {
	g := Grant{Project: project, Harness: harness, Pattern: strings.TrimSpace(pattern), CreatedAt: s.stamp()}
	if g.Pattern == "" {
		return Grant{}, false, fmt.Errorf("%w: grant pattern must not be empty", ErrInvalid)
	}
	added := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT id, created_at FROM project_grant WHERE project = ? AND harness = ? AND pattern = ?`, project, harness, g.Pattern).Scan(&g.ID, &g.CreatedAt)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var known int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM project WHERE name = ?`, project).Scan(&known); err != nil {
			return err
		}
		if known == 0 {
			return fmt.Errorf("%w: project %s", ErrNotFound, project)
		}
		res, err := tx.Exec(`INSERT INTO project_grant(project, harness, pattern, created_at) VALUES (?, ?, ?, ?)`, project, harness, g.Pattern, g.CreatedAt)
		if err != nil {
			return err
		}
		if g.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		added = true
		return emit(tx, g.CreatedAt, "grant.added", 0, project+": "+harness+" "+g.Pattern)
	})
	return g, added, err
}

func (s *Store) Grants(ctx context.Context, project, harness string) ([]Grant, error) {
	q, args := `SELECT id, project, harness, pattern, created_at FROM project_grant WHERE project = ?`, []any{project}
	if harness != "" {
		q += ` AND harness = ?`
		args = append(args, harness)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Project, &g.Harness, &g.Pattern, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) RemoveGrant(ctx context.Context, project string, id int64) (Grant, error) {
	var g Grant
	err := s.tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT id, project, harness, pattern, created_at FROM project_grant WHERE id = ? AND project = ?`, id, project).Scan(&g.ID, &g.Project, &g.Harness, &g.Pattern, &g.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: grant %d of project %s", ErrNotFound, id, project)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM project_grant WHERE id = ?`, id); err != nil {
			return err
		}
		return emit(tx, s.stamp(), "grant.removed", 0, project+": "+g.Harness+" "+g.Pattern)
	})
	return g, err
}
