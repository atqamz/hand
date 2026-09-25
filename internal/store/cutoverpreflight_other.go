//go:build !linux && !darwin && !windows

package store

import (
	"errors"
	"runtime"
)

var errLegacyV18CutoverUnsupportedPlatform = errors.New("offline cutover is unsupported on " + runtime.GOOS)

func legacyV18CutoverBootToken() (string, error) { return "", errLegacyV18CutoverUnsupportedPlatform }

func legacyV18CutoverMachineID() (string, error) { return "", errLegacyV18CutoverUnsupportedPlatform }

func classifyLegacyV18CutoverFilesystem(string) error { return errLegacyV18CutoverUnsupportedPlatform }
