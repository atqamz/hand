package state

import "github.com/atqamz/secondhand/internal/store"

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
