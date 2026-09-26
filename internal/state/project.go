package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type Project struct {
	Name      string
	Repo      string
	CreatedAt string
}

func (s *Store) AddProject(ctx context.Context, name, repo string) (Project, error) {
	if !projectName.MatchString(name) {
		return Project{}, fmt.Errorf("%w: project name %q must match %s", ErrInvalid, name, projectName)
	}
	if !filepath.IsAbs(repo) {
		return Project{}, fmt.Errorf("%w: repo %q must be an absolute path", ErrInvalid, repo)
	}
	p := Project{Name: name, Repo: filepath.Clean(repo), CreatedAt: s.stamp()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO project(name, repo, created_at) VALUES (?, ?, ?)`, p.Name, s.stored(p.Repo), p.CreatedAt); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: project %s already exists", ErrConflict, name)
			}
			return err
		}
		return emit(tx, p.CreatedAt, "project.added", 0, name)
	})
	return p, err
}

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, repo, created_at FROM project ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.Repo, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Repo = s.resolved(p.Repo)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Project(ctx context.Context, name string) (Project, error) {
	var p Project
	err := s.db.QueryRowContext(ctx, `SELECT name, repo, created_at FROM project WHERE name = ?`, name).Scan(&p.Name, &p.Repo, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: project %s", ErrNotFound, name)
	}
	p.Repo = s.resolved(p.Repo)
	return p, err
}

func (s *Store) stored(repo string) string {
	rel, err := filepath.Rel(s.home, repo)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return repo
	}
	return rel
}

func (s *Store) resolved(repo string) string {
	if filepath.IsAbs(repo) {
		return repo
	}
	return filepath.Join(s.home, repo)
}
