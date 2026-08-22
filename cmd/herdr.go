package cmd

import (
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func currentHerdrClient(home string) (*herdr.Client, error) {
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		if errors.Is(err, store.ErrFleetIdentityMissing) {
			return herdr.NewClient(), nil
		}
		return nil, fmt.Errorf("read Fleet identity: %w", err)
	}
	return herdr.NewSessionClient(herdr.SessionName(fleetID)), nil
}

func herdrClientForAttempt(attempt *state.Attempt, current *herdr.Client) *herdr.Client {
	if attempt == nil || attempt.Herdr.Session == "" || attempt.Herdr.Session == "default" {
		return herdr.NewClient()
	}
	return herdr.NewSessionClient(attempt.Herdr.Session)
}
