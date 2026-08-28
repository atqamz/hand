// Package home resolves the one secondhand fleet home a command runs
// against, replacing the scattered bare os.Getwd() calls each command used
// to make on its own.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/worktree"
)

// ErrNotFound is wrapped into the error Resolve returns when the working
// directory has no fleet home above it. It renders as the full user-facing
// sentence on its own, so callers should not add more context around it.
var ErrNotFound = errors.New("not inside a secondhand home; run `hand init` or set HAND_HOME")

// ErrHandHomeInvalid is wrapped into Resolve's error when HAND_HOME is set but does
// not name a fleet home. Separate from ErrNotFound because the remedy differs: an
// operator who already set HAND_HOME is not helped by being told to set it.
var ErrHandHomeInvalid = errors.New("is not a secondhand home; check the path or unset HAND_HOME to search up from the working directory")

// ErrAmbiguousHome is wrapped into Resolve's error when HAND_HOME names one fleet home and the
// working directory sits inside a different one (atqamz/hand#460). See
// docs/adr/two-fleet-homes-in-play-is-a-refusal.md for why neither side may win silently.
var ErrAmbiguousHome = errors.New("HAND_HOME and the working directory name two different fleet homes, with no stated precedence")

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
// ancestor of the working directory that IsHome accepts. A home differing from HAND_HOME refuses
// with ErrAmbiguousHome; see docs/adr/two-fleet-homes-in-play-is-a-refusal.md for why and at what cost.
func Resolve() (string, error) {
	handHome := os.Getenv("HAND_HOME")
	if handHome == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		found, err := nearestAncestorHome(cwd)
		if err != nil {
			return "", err
		}
		if found == "" {
			return "", ErrNotFound
		}
		return found, nil
	}

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

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	if cannotBeADifferentHome(cwd, handHome) {
		return handHome, nil
	}
	cwdHome, err := nearestAncestorHome(cwd)
	if err != nil {
		return "", err
	}
	if cwdHome != "" && cwdHome != handHome {
		return "", fmt.Errorf(
			"%w: HAND_HOME is %q, the working directory is inside %q; unset HAND_HOME to act on the working directory's home, or run from outside %q to act on HAND_HOME's",
			ErrAmbiguousHome, handHome, cwdHome, cwdHome)
	}
	return handHome, nil
}

// Walks up from dir, itself included, returning the first ancestor IsHome accepts, or "" if none
// does; a caller turns that into ErrNotFound or into "no cwd home to conflict with HAND_HOME".
func nearestAncestorHome(dir string) (string, error) {
	for {
		ok, err := IsHome(dir)
		if err != nil {
			return "", err
		}
		if ok {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// Reports, without a single stat call, whether cwd is provably not a fleet home other than
// handHome: either cwd sits inside handHome, or inside the Treehouse pool (INV-POOL-1, INV-REG-7).
// A "no" answer, including one from a PoolsRoot lookup failure, only sends the caller to the walk.
func cannotBeADifferentHome(cwd, handHome string) bool {
	if withinTree(handHome, cwd) {
		return true
	}
	pools, err := worktree.PoolsRoot()
	if err != nil {
		return false
	}
	return withinTree(pools, cwd)
}

// Reports whether path is root or sits inside it, comparing cleaned absolute strings without
// resolving symlinks - the same rule cmd's own pool-membership check uses.
func withinTree(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
