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

// Parse reads path for a leading "---"-delimited front matter block declaring model and/or
// effort. present reports whether such a block was found at all, independent of whether it
// declared any recognized key - the harness prompt uses it to warn a worker away from reading
// the block as task content. A brief whose first line is not exactly "---", or whose opening
// "---" is never closed, is unmodified prose: Parse returns a zero Declaration and
// present=false rather than guess. Every existing brief in this fleet starts with a "#" heading
// or plain prose, never "---", so all of them parse exactly as they did before this existed.
// Unknown keys inside the block are ignored, not an error: a brief is prose that happens to
// carry two optional settings, not a config file that happens to contain prose.
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
	// Opening "---" never closed: not a real front matter block, just a line that looked
	// like one. Treat the whole file as prose rather than swallowing it as a declaration.
	return Declaration{}, false, nil
}
