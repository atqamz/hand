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

// Parse ignores unknown keys inside the block and reports "no declaration" for a brief it
// cannot scan (an unterminated fence, a line past bufio's token cap) by choice: a brief is
// prose carrying two optional settings, not a config file, so its shape may not fail a spawn.
func Parse(path string) (Declaration, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Declaration{}, false, fmt.Errorf("open brief %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Declaration{}, false, nil
	}

	var d Declaration
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			return d, true, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model":
			d.Model = unquote(value)
		case "effort":
			d.Effort = unquote(value)
		}
	}
	return Declaration{}, false, nil
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}
