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

// ErrHandHomeInvalid is wrapped into the error Resolve returns when HAND_HOME
// is set but does not name a fleet home. It is separate from ErrNotFound
// because the remedy differs: an operator who already set HAND_HOME is not
// helped by being told to set it.
var ErrHandHomeInvalid = errors.New("is not a secondhand home; check the path or unset HAND_HOME to search up from the working directory")

// IsHome reports whether dir is a fleet home, the one definition every caller
// (the resolver, hand init, and agentsmd's refresh) shares. The marker is
// data/dashboard.md, which only hand init writes, rather than the data/
// directory itself: project clones live at <home>/projects/<name>, so a clone
// carrying its own generic top-level data/ and state/ directories would
// otherwise stop the ancestor walk short and be dispatched into as the home.
func IsHome(dir string) (bool, error) {
	markers := []struct {
		rel   string
		isDir bool
	}{
		{filepath.Join("data", "dashboard.md"), false},
		{"state", true},
	}
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

// Resolve finds the fleet home a command should run against: HAND_HOME if
// set, otherwise the nearest ancestor of the working directory (including
// the working directory itself) that IsHome reports true for. HAND_HOME set
// to a directory that isn't a home fails loudly instead of silently falling
// back to the walk-up, since a silent fallback is how an operator ends up
// dispatching into the wrong fleet. The returned path is always absolute:
// commands derive paths they hand to subprocesses running elsewhere from it,
// and a relative HAND_HOME would otherwise name a different home per
// working directory.
func Resolve() (string, error) {
	if handHome := os.Getenv("HAND_HOME"); handHome != "" {
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
