package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const FleetIdentityLock = "fleet-identity"

var (
	ErrFleetIdentityMissing = errors.New("fleet identity missing")
	ErrFleetIdentityInvalid = errors.New("invalid fleet identity")
)

func (db *DB) FleetID() (string, error) {
	if db.empty {
		return "", ErrFleetIdentityMissing
	}
	var id string
	if err := db.sql.QueryRow(`SELECT fleet_id FROM fleet_identity WHERE singleton = 1`).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no such table: fleet_identity") {
			return "", ErrFleetIdentityMissing
		}
		return "", fmt.Errorf("read fleet identity: %w", err)
	}
	if err := validateFleetID(id); err != nil {
		return "", err
	}
	return id, nil
}

func FleetIDReadOnly(homeDir string) (string, error) {
	db, _, err := openReadOnlyForLifecycle(homeDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	return db.FleetID()
}

func ensureFleetIdentityTx(tx *sql.Tx) error {
	var id string
	err := tx.QueryRow(`SELECT fleet_id FROM fleet_identity WHERE singleton = 1`).Scan(&id)
	if err == nil {
		return validateFleetID(id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read fleet identity: %w", err)
	}
	id, err = newFleetID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO fleet_identity (singleton, fleet_id) VALUES (1, ?)`, id); err != nil {
		return fmt.Errorf("insert fleet identity: %w", err)
	}
	return nil
}

func newFleetID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate fleet identity: %w", err)
	}
	return "f_" + hex.EncodeToString(raw[:]), nil
}

func validateFleetID(id string) error {
	if len(id) != 34 || id[:2] != "f_" {
		return fmt.Errorf("%w: %q", ErrFleetIdentityInvalid, id)
	}
	for _, char := range id[2:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return fmt.Errorf("%w: %q", ErrFleetIdentityInvalid, id)
		}
	}
	return nil
}

func ValidateFleetID(id string) error {
	return validateFleetID(id)
}
