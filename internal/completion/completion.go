// Package completion is the durable record of tasks hand teardown has finished
// with: state/completions.jsonl, one JSON object per line, uncapped. It exists
// because data/dashboard.md's Recent Completions section is capped at 10 and is
// a rendering choice, not storage - see atqamz/secondhand#61. Every line is
// readable on its own terms without parsing markdown.
package completion

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/secondhand/internal/state"
)

// Record is one task's teardown outcome. Fields mirror dashboard.Completion,
// which stays the input to the capped render; TornDownAt is the one addition
// the durable store needs and the render does not.
type Record struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	Kind       string `json:"kind"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail"`
	TornDownAt string `json:"torndown_at"`
}

func Path(homeDir string) string {
	return filepath.Join(state.Dir(homeDir), "completions.jsonl")
}

// Append adds r as the store's newest line. Concurrent Append calls, including
// from other hand processes, stay intact because of what this deliberately does
// not do: state/events.log's appendEventLog reads the whole file, adds a line,
// and atomically replaces it, so two writers racing that read-modify-write can
// each read the same old content and one's line clobbers the other's on
// rename. Append instead takes the same named-lock primitive state/<id>.json's
// writers use, serializing against any other Append, and writes one complete
// line with a single O_APPEND syscall - no read, no rename, nothing for a
// second writer to race.
func Append(homeDir string, r Record) error {
	release, err := state.Lock(homeDir, "completions")
	if err != nil {
		return fmt.Errorf("lock completions store: %w", err)
	}
	defer release()

	if err := os.MkdirAll(state.Dir(homeDir), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode completion record %q: %w", r.ID, err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(Path(homeDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open completions store: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append completion record %q: %w", r.ID, err)
	}
	return nil
}

// List returns every record in the store, oldest first. Returns nil if the
// store doesn't exist yet.
func List(homeDir string) ([]Record, error) {
	f, err := os.Open(Path(homeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open completions store: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parse completions store: %w", err)
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read completions store: %w", err)
	}
	return records, nil
}
