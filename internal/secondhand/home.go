// Package secondhand resolves the user-local infrastructure root shared by Hand components.
package secondhand

import (
	"fmt"
	"os"
	"path/filepath"
)

func Home() (string, error) {
	if configured := os.Getenv("SECONDHAND_HOME"); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve SECONDHAND_HOME: %w", err)
		}
		return filepath.Clean(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for Secondhand infrastructure: %w", err)
	}
	return filepath.Join(home, ".secondhand"), nil
}
