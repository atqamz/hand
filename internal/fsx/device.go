package fsx

import "fmt"

func SameDevice(a, b string) (bool, error) {
	da, err := Device(a)
	if err != nil {
		return false, fmt.Errorf("fsx: device of %s: %w", a, err)
	}
	db, err := Device(b)
	if err != nil {
		return false, fmt.Errorf("fsx: device of %s: %w", b, err)
	}
	return da == db, nil
}
