// Package completion is the durable record of tasks hand teardown has finished
// with: state/completions.jsonl, one JSON object per line, uncapped - see
// atqamz/hand#61. Normal records are appended; project rename may atomically
// rewrite the matching records. Every line is readable on its own terms
// without parsing markdown.
package completion

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/state"
)

// Record is one task's teardown outcome.
type Record struct {
	ID               string `json:"id"`
	Project          string `json:"project"`
	Kind             string `json:"kind"`
	Outcome          string `json:"outcome"`
	Detail           string `json:"detail"`
	TornDownAt       string `json:"torndown_at"`
	AttemptID        int64  `json:"attempt_id,omitempty"`
	AttemptLifecycle string `json:"attempt_lifecycle,omitempty"`
}

func Path(homeDir string) string {
	return filepath.Join(state.Dir(homeDir), "completions.jsonl")
}

// Append adds r as the store's newest line. Concurrent calls, including from other
// hand processes, stay intact by avoiding events.log's read-modify-write-rename race:
// a named lock plus one O_APPEND write leaves a second writer nothing to clobber.
func Append(homeDir string, r Record) error {
	release, err := state.Lock(homeDir, "completions")
	if err != nil {
		return fmt.Errorf("lock completions store: %w", err)
	}
	defer release()

	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode completion record %q: %w", r.ID, err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(Path(homeDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open completions store: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("append completion record %q: %w", r.ID, err)
	}
	// Not deferred and not discarded: a filesystem that reports a write fault only
	// at close (NFS, delayed-allocation ENOSPC) makes Close the sole signal the record
	// reached disk, and teardown terminalizes the task and its attempt on this returning nil.
	if err := f.Close(); err != nil {
		return fmt.Errorf("close completions store: %w", err)
	}
	return nil
}

// RenameRestore restores only the completion lines changed by RenameProject.
type RenameRestore struct {
	homeDir string
	changes []renameChange
}

type renameChange struct {
	line         int
	originalLine string
	renamedLine  string
}

// Restore reverts the exact lines changed by the rename, if they are still unchanged.
func (r RenameRestore) Restore() error {
	if len(r.changes) == 0 {
		return nil
	}
	release, err := state.Lock(r.homeDir, "completions")
	if err != nil {
		return fmt.Errorf("lock completions store: %w", err)
	}
	defer release()

	path := Path(r.homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read completions store for rollback: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat completions store for rollback: %w", err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	for _, change := range r.changes {
		if change.line >= len(lines) || lines[change.line] != change.renamedLine {
			return fmt.Errorf("completion line %d changed during rollback", change.line)
		}
		lines[change.line] = change.originalLine
	}
	if err := atomicfile.Write(path, ".completions-rollback-", []byte(strings.Join(lines, "")), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write completions store for rollback: %w", err)
	}
	return nil
}

func RenameProject(homeDir, oldName, newName string) (RenameRestore, error) {
	release, err := state.Lock(homeDir, "completions")
	if err != nil {
		return RenameRestore{}, fmt.Errorf("lock completions store: %w", err)
	}
	defer release()

	path := Path(homeDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return RenameRestore{}, nil
	}
	if err != nil {
		return RenameRestore{}, fmt.Errorf("read completions store: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return RenameRestore{}, fmt.Errorf("stat completions store: %w", err)
	}

	var rendered strings.Builder
	var changes []renameChange
	for lineNumber, line := range strings.SplitAfter(string(data), "\n") {
		body, newline := strings.CutSuffix(line, "\n")
		var record Record
		if err := json.Unmarshal([]byte(body), &record); err == nil && record.Project == oldName {
			record.Project = newName
			encoded, err := json.Marshal(record)
			if err != nil {
				return RenameRestore{}, fmt.Errorf("encode completion record %q: %w", record.ID, err)
			}
			renameLine := string(encoded)
			if newline {
				renameLine += "\n"
			}
			rendered.WriteString(renameLine)
			changes = append(changes, renameChange{line: lineNumber, originalLine: line, renamedLine: renameLine})
			continue
		}
		rendered.WriteString(line)
	}
	if len(changes) == 0 {
		return RenameRestore{}, nil
	}
	if err := atomicfile.Write(path, ".completions-", []byte(rendered.String()), info.Mode().Perm()); err != nil {
		return RenameRestore{}, fmt.Errorf("write completions store: %w", err)
	}
	return RenameRestore{homeDir: homeDir, changes: changes}, nil
}

// List returns every record, oldest first, and nil if the store doesn't exist yet. A
// line that does not parse is skipped: a short write leaves a partial line the next
// O_APPEND glues onto, and one damaged line must not hide the good records before it.
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
			continue
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read completions store: %w", err)
	}
	return records, nil
}

// Recovery lookup reads the append-only audit file only for a known teardown
// attempt. An absent AttemptID is intentionally never inferred.
func FindAttempt(homeDir string, attemptID int64) (Record, bool, error) {
	if attemptID == 0 {
		return Record{}, false, nil
	}
	f, err := os.Open(Path(homeDir))
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("open completions store: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil || record.AttemptID != attemptID {
			continue
		}
		return record, true, nil
	}
	if err := scanner.Err(); err != nil {
		return Record{}, false, fmt.Errorf("read completions store: %w", err)
	}
	return Record{}, false, nil
}
