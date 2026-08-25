// Package completion is the durable record of tasks hand teardown has finished
// with: state/completions.jsonl, one JSON object per line, uncapped - see
// atqamz/hand#61. Records are appended and never mutated in the ordinary course;
// the one exception is the one-time project-identity migration below, which
// replaces the whole file or none of it. Every line is readable on its own terms
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

// RecordVersion 2 is the first shape that carries a project identity. A line without the field
// reads as version 1, which named its project by a label that a later rename or a reuse of that
// name could make mean something else (atqamz/hand#388).
const RecordVersion = 2

// ProjectIDUnknown is what a record carries when nothing it holds can name the project it came
// from. It is deliberately visible rather than empty, so an ambiguous record stays ambiguous
// instead of reading as one belonging to whatever project now holds the name it was written with.
const ProjectIDUnknown = "unknown"

// Record is one task's teardown outcome. Project is the label the project carried when the record
// was written and is never rewritten afterwards; ProjectID is the identity a rename cannot change,
// and is what a reader joins on.
type Record struct {
	Version          int    `json:"record_version,omitempty"`
	ID               string `json:"id"`
	Project          string `json:"project"`
	ProjectID        string `json:"project_id,omitempty"`
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

	r.Version = RecordVersion
	if r.ProjectID == "" {
		r.ProjectID = ProjectIDUnknown
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

// ProjectIdentityResolver answers which project a legacy record belongs to. An empty answer means
// the lineage is not knowable from what the record carries, and the migration writes
// ProjectIDUnknown rather than attaching the record to whatever project now holds its name.
type ProjectIdentityResolver func(Record) string

// MigrateProjectIdentity is the one-time format bump that gives each existing line the identity
// field, in place of a schema migration this file cannot take part in. The whole file is replaced
// through a temp file and a rename or nothing is written; a current line is passed through as is.
func MigrateProjectIdentity(homeDir string, resolve ProjectIdentityResolver) error {
	release, err := state.Lock(homeDir, "completions")
	if err != nil {
		return fmt.Errorf("lock completions store: %w", err)
	}
	defer release()

	path := Path(homeDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read completions store: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat completions store: %w", err)
	}

	var rendered strings.Builder
	migrated := 0
	for _, line := range strings.SplitAfter(string(data), "\n") {
		body, newline := strings.CutSuffix(line, "\n")
		var record Record
		// A line that does not parse is another writer's partial append or damage: it is carried
		// through verbatim for the same reason List skips it rather than dropping the file.
		if err := json.Unmarshal([]byte(body), &record); err != nil || record.Version >= RecordVersion {
			rendered.WriteString(line)
			continue
		}
		record.Version = RecordVersion
		record.ProjectID = resolve(record)
		if record.ProjectID == "" {
			record.ProjectID = ProjectIDUnknown
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode completion record %q: %w", record.ID, err)
		}
		rendered.Write(encoded)
		if newline {
			rendered.WriteString("\n")
		}
		migrated++
	}
	if migrated == 0 {
		return nil
	}
	if err := atomicfile.Write(path, ".completions-identity-", []byte(rendered.String()), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write completions store: %w", err)
	}
	return nil
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
