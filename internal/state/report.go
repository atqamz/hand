package state

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// A different question from the task's own age, which is why hand status shows
// both: a task spawned hours ago whose worker reported a minute ago is healthy,
// and one age column cannot say that.
func ReportModTime(homeDir, id string) (time.Time, bool, error) {
	info, err := os.Stat(ReportPath(homeDir, id))
	if os.IsNotExist(err) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("stat report %q: %w", id, err)
	}
	return info.ModTime(), true, nil
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

// tailReportBytes reads whatever a task's report file holds past offset, and
// returns the offset it actually read from. Rotation/truncation aren't
// supported; if the file has shrunk below offset, reading restarts from the
// beginning.
func tailReportBytes(path string, offset int64) ([]byte, int64, error) {
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
	return data, offset, nil
}

// TailReport reads whatever complete lines have been appended to a task's
// report file since offset, returning them alongside the new offset to persist.
// A trailing line with no terminating newline is left unconsumed in case the
// worker's append is still in flight.
func TailReport(path string, offset int64) ([]ReportLine, int64, error) {
	data, base, err := tailReportBytes(path, offset)
	if err != nil {
		return nil, base, err
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
	return lines, base + int64(consumed), nil
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
	return classifyReportBytes(data), nil
}

// classifyReportBytes classifies every line in data, a trailing line with no
// terminating newline included. That last line is the difference between a
// snapshot reader and TailReport: a watcher leaves it for its next tick because
// it will still be there, while a reader answering a question about right now
// has to account for it.
func classifyReportBytes(data []byte) []ReportLine {
	var lines []ReportLine
	for _, l := range strings.Split(string(data), "\n") {
		if blankReportLine(l) {
			continue
		}
		lines = append(lines, ParseReportLine(l))
	}
	return lines
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

// TerminalReport reports whether s is a state a worker does not carry on past
// under its own steam, so that someone has to hear about it.
func TerminalReport(s string) bool {
	return s == ReportDone || s == ReportFailed
}

// UnacknowledgedTerminalReport reports a terminal state no hand watch has ever
// consumed, from the task's durable report_offset. That offset is the marker: the
// poll loop advances it only after the tick's events are announced, and every
// announcement reaches state/events.log and the notify hook, so a terminal line
// still past it has reached nobody (atqamz/secondhand#70).
//
// Only the last classified line of that unconsumed tail counts. A terminal
// report a worker has since superseded with more work needs no acknowledging,
// and a done report is routinely followed by more work on the same worker.
//
// It classifies its own tail rather than calling TailReport, so that a terminal
// line with no terminating newline counts too: TailReport leaves that line for
// the watcher's next tick, and a report the watcher will not announce until then
// is precisely one that has reached nobody yet. Skipping it would let the silent
// completion this exists to surface back in through the newline.
//
// A watcher denied the task lock announces a line and persists the offset a tick
// later, so the transient error here is reporting an acknowledged terminal state,
// never hiding an unacknowledged one.
func UnacknowledgedTerminalReport(homeDir, id string, offset int64) (bool, error) {
	data, _, err := tailReportBytes(ReportPath(homeDir, id), offset)
	if err != nil {
		return false, err
	}
	last, ok := LastReportedState(classifyReportBytes(data))
	if !ok {
		return false, nil
	}
	return TerminalReport(last.State), nil
}
