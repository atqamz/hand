package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func observeCurrentHerdrSession(ctx context.Context, home string) herdr.SessionObservation {
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		return herdr.SessionObservation{State: herdr.SessionUnknown, Reason: "read Fleet identity: " + err.Error()}
	}
	return herdr.ObserveFleetSession(ctx, fleetID)
}

func currentHerdrClient(home string) (*herdr.Client, error) {
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		if errors.Is(err, store.ErrFleetIdentityMissing) {
			return herdr.NewManagedClient(), nil
		}
		return nil, fmt.Errorf("read Fleet identity: %w", err)
	}
	return herdr.NewManagedSessionClient(herdr.SessionName(fleetID)), nil
}

func herdrClientForAttempt(attempt *state.Attempt, current *herdr.Client) *herdr.Client {
	if attempt == nil || attempt.Herdr.Session == "" || attempt.Herdr.Session == "default" {
		return herdr.NewManagedClient()
	}
	return herdr.NewManagedSessionClient(attempt.Herdr.Session)
}
