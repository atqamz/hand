package runtime

import "github.com/atqamz/hand/internal/registry"

func fleetPreflight(home string) error {
	_, err := registry.Preflight(home, false)
	return err
}

func fleetPreflightReadOnly(home string) error {
	_, err := registry.Preflight(home, true)
	return err
}
