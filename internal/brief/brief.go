// Package brief reads the optional model/effort tier a brief.md declares for itself.
package brief

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Declaration struct {
	Model  string
	Effort string
}

// Parse ignores unknown keys inside the block by choice, not oversight: a brief is prose
// carrying two optional settings, not a config file.
func Parse(path string) (Declaration, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Declaration{}, false, fmt.Errorf("open brief %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Declaration{}, false, scanner.Err()
	}

	var d Declaration
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			return d, true, scanner.Err()
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model":
			d.Model = strings.TrimSpace(value)
		case "effort":
			d.Effort = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Declaration{}, false, err
	}
	return Declaration{}, false, nil
}
