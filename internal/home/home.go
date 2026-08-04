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

// ErrNotFound is wrapped into the error Resolve returns when the working
// directory has no fleet home above it. It renders as the full user-facing
// sentence on its own, so callers should not add more context around it.
var ErrNotFound = errors.New("not inside a secondhand home; run `hand init` or set HAND_HOME")

// ErrHandHomeInvalid is wrapped into Resolve's error when HAND_HOME is set but does
// not name a fleet home. Separate from ErrNotFound because the remedy differs: an
// operator who already set HAND_HOME is not helped by being told to set it.
var ErrHandHomeInvalid = errors.New("is not a secondhand home; check the path or unset HAND_HOME to search up from the working directory")

// IsHome reports whether dir is a fleet home, the one definition the resolver, hand
// init and agentsmd's refresh share. The marker is state/hand.db, not state/ itself: a
// clone under projects/ with generic data/ and state/ would be dispatched into as home.
func IsHome(dir string) (bool, error) {
	for _, markers := range markerSets {
		ok, err := matchesMarkers(dir, markers)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

type homeMarker struct {
	rel   string
	isDir bool
}

// hand.db is only ever created by hand: hand init writes it up front and every command
// touching machine state recreates it. The second set is the pre-hand.db marker, so an
// older home upgrades in place and internal/store's migrateLegacy still finds it.
var markerSets = [][]homeMarker{
	{
		{"state", true},
		{filepath.Join("state", "hand.db"), false},
	},
	{
		{"data", true},
		{filepath.Join("data", "projects.md"), false},
		{"state", true},
	},
}

// Relies on every marker set listing a directory ahead of the markers nested under it,
// so a parent that turned out to be a plain file is rejected before the nested stat -
// which would fail with "not a directory" instead of the not-exist read as no-match.
func matchesMarkers(dir string, markers []homeMarker) (bool, error) {
	for _, m := range markers {
		info, err := os.Stat(filepath.Join(dir, m.rel))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("check %s: %w", m.rel, err)
		}
		if info.IsDir() != m.isDir {
			return false, nil
		}
	}
	return true, nil
}

// Resolve finds the fleet home a command runs against: HAND_HOME if set, else the nearest
// ancestor of the working directory (itself included) that IsHome accepts. A HAND_HOME that
// is not a home fails loudly, because a silent walk-up dispatches into the wrong fleet.
func Resolve() (string, error) {
	if handHome := os.Getenv("HAND_HOME"); handHome != "" {
		// Absolute, because commands derive the paths they hand to subprocesses running
		// elsewhere from this, and a relative HAND_HOME would name a different home for
		// every working directory it is read from.
		handHome, err := filepath.Abs(handHome)
		if err != nil {
			return "", fmt.Errorf("resolve HAND_HOME: %w", err)
		}
		ok, err := IsHome(handHome)
		if err != nil {
			return "", fmt.Errorf("check HAND_HOME %q: %w", handHome, err)
		}
		if !ok {
			return "", fmt.Errorf("HAND_HOME %q %w", handHome, ErrHandHomeInvalid)
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
