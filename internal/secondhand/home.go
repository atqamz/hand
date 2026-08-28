// Package secondhand resolves the user-local infrastructure root shared by Hand components.
package secondhand

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/testtag"
)

// ErrHomeNotOverridden is wrapped into Home's error when a test build finds no SECONDHAND_HOME set.
// A single missed override once cost the operator's real registry 3366 dead rows (atqamz/hand#413):
// the fallback below must be unreachable from a test binary, not merely unused by convention.
var ErrHomeNotOverridden = errors.New("SECONDHAND_HOME is not set")

func Home() (string, error) {
	configured := os.Getenv("SECONDHAND_HOME")
	if configured == "" {
		if testtag.Present {
			return "", fmt.Errorf("%w: a test build must not resolve the operator's real Secondhand infrastructure root; set SECONDHAND_HOME before any code that reaches it runs (`make test`, `make e2e` and this repo's package TestMains already do)", ErrHomeNotOverridden)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for Secondhand infrastructure: %w", err)
		}
		return filepath.Join(home, ".secondhand"), nil
	}
	path, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve SECONDHAND_HOME: %w", err)
	}
	return filepath.Clean(path), nil
}
