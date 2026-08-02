// Package home resolves the one secondhand fleet home a command runs
// against, replacing the scattered bare os.Getwd() calls each command used
// to make on its own.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound is wrapped into the error Resolve returns when no directory
// from the resolution order qualifies as a fleet home. It renders as the
// full user-facing sentence on its own, so callers should not add more
// context around it.
var ErrNotFound = errors.New("not inside a secondhand home; run `hand init` or set HAND_HOME")

// IsHome reports whether dir holds both data/ and state/ as directories,
// the one definition of a fleet home every caller (the resolver, hand init,
// and agentsmd's refresh) shares.
func IsHome(dir string) (bool, error) {
	for _, sub := range []string{"data", "state"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("check %s: %w", sub, err)
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

// Resolve finds the fleet home a command should run against: HAND_HOME if
// set, otherwise the nearest ancestor of the working directory (including
// the working directory itself) that IsHome reports true for. HAND_HOME set
// to a directory that isn't a home fails loudly instead of silently falling
// back to the walk-up, since a silent fallback is how an operator ends up
// dispatching into the wrong fleet.
func Resolve() (string, error) {
	if handHome := os.Getenv("HAND_HOME"); handHome != "" {
		ok, err := IsHome(handHome)
		if err != nil {
			return "", fmt.Errorf("check HAND_HOME %q: %w", handHome, err)
		}
		if !ok {
			return "", fmt.Errorf("HAND_HOME %q: %w", handHome, ErrNotFound)
		}
		return handHome, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for dir := cwd; ; {
		ok, err := IsHome(dir)
		if err != nil {
			return "", err
		}
		if ok {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}
