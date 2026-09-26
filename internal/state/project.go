package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
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
	real := repo
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		real = r
	}
	rel, err := filepath.Rel(s.home, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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

func (s *Store) RebaseProjects(ctx context.Context, from string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT name, repo FROM project ORDER BY name`)
		if err != nil {
			return err
		}
		moved := map[string]string{}
		for rows.Next() {
			var name, repo string
			if err := rows.Scan(&name, &repo); err != nil {
				_ = rows.Close()
				return err
			}
			rel, err := filepath.Rel(from, repo)
			if filepath.IsAbs(repo) && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				moved[name] = rel
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		at := s.stamp()
		for _, name := range slices.Sorted(maps.Keys(moved)) {
			if _, err := tx.Exec(`UPDATE project SET repo = ? WHERE name = ?`, moved[name], name); err != nil {
				return err
			}
			if err := emit(tx, at, "project.rebased", 0, name); err != nil {
				return err
			}
		}
		return nil
	})
}
