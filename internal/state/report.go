package state

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Report states are a fixed vocabulary: free text after the state can't be
// classified, so hand only trusts these six prefixes when reading
// state/<id>.status.
const (
	ReportWorking       = "working"
	ReportPaused        = "paused"
	ReportBlocked       = "blocked"
	ReportNeedsDecision = "needs-decision"
	ReportDone          = "done"
	ReportFailed        = "failed"
)

var reportStates = map[string]bool{
	ReportWorking:       true,
	ReportPaused:        true,
	ReportBlocked:       true,
	ReportNeedsDecision: true,
	ReportDone:          true,
	ReportFailed:        true,
}

// ReportPath returns the path to a task's reported-state channel. The worker
// appends to it via plain shell redirection (its absolute path belongs in the
// brief the supervisor writes) - hand only ever reads it, never writes it.
func ReportPath(homeDir, id string) string {
	return filepath.Join(Dir(homeDir), id+".status")
}

// ReportLine is one classified line from a task's report file.
type ReportLine struct {
	State     string // one of the vocabulary constants above, empty if Malformed
	Note      string // free text after the first colon, verbatim
	Raw       string
	Malformed bool
}

// ParseReportLine classifies line by its "<state>: <note>" prefix. A line whose
// prefix isn't in the fixed vocabulary is malformed and must be surfaced, never
// silently dropped, since free text on its own can't be classified.
func ParseReportLine(line string) ReportLine {
	prefix, note, ok := strings.Cut(line, ":")
	if ok {
		prefix = strings.TrimSpace(prefix)
		note = strings.TrimSpace(note)
	}
	if !ok || !reportStates[prefix] {
		return ReportLine{Raw: line, Malformed: true}
	}
	return ReportLine{State: prefix, Note: note, Raw: line}
}

// TailReport reads whatever complete lines have been appended to a task's
// report file since offset, returning them alongside the new offset to persist.
// A trailing line with no terminating newline is left unconsumed in case the
// worker's append is still in flight. Rotation/truncation aren't supported; if
// the file has shrunk below offset, tailing restarts from the beginning.
func TailReport(path string, offset int64) ([]ReportLine, int64, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, offset, fmt.Errorf("open report %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, fmt.Errorf("stat report %s: %w", path, err)
	}
	if info.Size() < offset {
		offset = 0
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, fmt.Errorf("seek report %s: %w", path, err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, offset, fmt.Errorf("read report %s: %w", path, err)
	}

	var lines []ReportLine
	consumed := 0
	for {
		idx := bytes.IndexByte(data[consumed:], '\n')
		if idx == -1 {
			break
		}
		line := string(data[consumed : consumed+idx])
		consumed += idx + 1
		if !blankReportLine(line) {
			lines = append(lines, ParseReportLine(line))
		}
	}
	return lines, offset + int64(consumed), nil
}

// ReadReportLines reads and classifies every line currently in a task's report
// file, for stateless consumers like hand status that don't track an offset
// across invocations.
func ReadReportLines(homeDir, id string) ([]ReportLine, error) {
	data, err := os.ReadFile(ReportPath(homeDir, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read report %q: %w", id, err)
	}
	var lines []ReportLine
	for _, l := range strings.Split(string(data), "\n") {
		if blankReportLine(l) {
			continue
		}
		lines = append(lines, ParseReportLine(l))
	}
	return lines, nil
}

// blankReportLine is the skip rule both readers share, so hand status never
// shows an entry hand watch didn't surface. Whitespace-only counts as blank:
// otherwise a trailing "  \n" would become the last (malformed) report and
// mask a real terminal report sitting right above it.
func blankReportLine(line string) bool {
	return strings.TrimSpace(line) == ""
}

// LastReportedState returns the most recent line in lines whose state classified -
// the last thing the worker actually said about itself - skipping trailing malformed
// lines rather than letting one erase it. Free text explains nothing, so a worker
// that appends some after a real report has still reported: this is the rule the
// live classifier follows (see ClassifyReportLine), and a reader that recovers the
// last known report state from the file has to reach the same answer.
//
// It takes lines rather than reading the file so a caller that also needs the raw
// last line gets both from one read. Two reads are two snapshots, and a worker
// appending between them would have one row's raw line and its own classified state
// describing different reports - the same two-views-disagree defect the report
// channel exists to remove.
func LastReportedState(lines []ReportLine) (ReportLine, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if !lines[i].Malformed {
			return lines[i], true
		}
	}
	return ReportLine{}, false
}

func ReportTail(homeDir, id string, n int) ([]ReportLine, error) {
	lines, err := ReadReportLines(homeDir, id)
	if err != nil {
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
