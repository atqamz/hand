package state

import (
	"errors"

	"github.com/atqamz/secondhand/internal/store"
)

// ErrHoldNotFound is wrapped into errors returned by ClearHold when no hold
// row exists for the given ID, rendering as `hold "<id>" not found`.
var ErrHoldNotFound = store.ErrHoldNotFound

func SetHold(homeDir string, h Hold) error {
	if err := ValidateID(h.ID); err != nil {
		return err
	}
	if h.Kind == HoldKindBlocked {
		if err := ValidateID(h.BlockedOn); err != nil {
			return err
		}
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.SetHold(h)
}

func ClearHold(homeDir, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.ClearHold(id)
}

// ClearHoldIfKind drops the hold on id only when it is of kind, so a caller that
// invalidated one machine-set hold decides nothing about an operator's own hold on the
// same id. A missing hold is the ordinary case, not a failure.
func ClearHoldIfKind(homeDir, id, kind string) error {
	h, exists, err := ReadHold(homeDir, id)
	if err != nil || !exists || h.Kind != kind {
		return err
	}
	if err := ClearHold(homeDir, id); err != nil && !errors.Is(err, ErrHoldNotFound) {
		return err
	}
	return nil
}

func ReadHold(homeDir, id string) (Hold, bool, error) {
	if err := ValidateID(id); err != nil {
		return Hold{}, false, err
	}
	db, err := store.Open(homeDir)
	if err != nil {
		return Hold{}, false, err
	}
	defer func() { _ = db.Close() }()
	return db.ReadHold(id)
}

func ListHolds(homeDir string) ([]Hold, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListHolds()
}
