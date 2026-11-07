package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var FleetID = regexp.MustCompile(`^f[0-9a-f]{12}$`)

type Fleet struct {
	ID   string
	Name string
	Home string
}

var errNoFleet = fmt.Errorf("%w: this home has no fleet identity; run `hand init`", ErrNotFound)

func ValidFleetName(name string) error {
	if name == "" || len(name) > 64 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("%w: fleet name %q must be 1-64 bytes of printable text", ErrInvalid, name)
	}
	return nil
}

func (s *Store) Fleet(ctx context.Context) (Fleet, error) {
	var f Fleet
	err := s.db.QueryRowContext(ctx, `SELECT id, name, home FROM fleet`).Scan(&f.ID, &f.Name, &f.Home)
	if err != nil && strings.Contains(err.Error(), "no such column: home") {
		err = s.db.QueryRowContext(ctx, `SELECT id, name FROM fleet`).Scan(&f.ID, &f.Name)
		f.Home = ""
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Fleet{}, errNoFleet
	}
	return f, err
}

func (s *Store) CreateFleet(ctx context.Context, name string) (Fleet, error) {
	if err := ValidFleetName(name); err != nil {
		return Fleet{}, err
	}
	b := make([]byte, 6)
	rand.Read(b)
	f := Fleet{ID: "f" + hex.EncodeToString(b), Name: name, Home: s.home}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO fleet(only, id, name, home) VALUES (1, ?, ?, ?)`, f.ID, f.Name, s.home); err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: this home already has a fleet identity", ErrConflict)
			}
			return err
		}
		return emit(tx, s.stamp(), "fleet.created", 0, f.ID+" "+f.Name)
	})
	return f, err
}

func (s *Store) RenameFleet(ctx context.Context, name string) (Fleet, error) {
	if err := ValidFleetName(name); err != nil {
		return Fleet{}, err
	}
	var f Fleet
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		f, err = renameFleet(ctx, tx, s.stamp(), name)
		return err
	})
	return f, err
}

func renameFleet(ctx context.Context, tx *sql.Tx, at, name string) (Fleet, error) {
	if err := ValidFleetName(name); err != nil {
		return Fleet{}, err
	}
	var f Fleet
	err := tx.QueryRowContext(ctx, `UPDATE fleet SET name = ? RETURNING id, name, home`, name).Scan(&f.ID, &f.Name, &f.Home)
	if errors.Is(err, sql.ErrNoRows) {
		return Fleet{}, errNoFleet
	}
	if err != nil {
		return Fleet{}, err
	}
	if err := emit(tx, at, "fleet.renamed", 0, f.ID+" "+f.Name); err != nil {
		return Fleet{}, err
	}
	return f, nil
}
