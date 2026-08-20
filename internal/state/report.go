package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// How far a task's report channel has been consumed. A worker reporting with a truncating redirect
// rewrites the file in place rather than appending, and a rewrite whose length happens to equal the
// offset leaves every quantity the offset alone can offer identical (atqamz/hand#149).
type ReportCursor struct {
	Offset int64
	// What tells that same-length rewrite apart: it still sits just past a final newline with nothing
	// after it, so only a fingerprint of the covered bytes decides whether Offset still means
	// anything - without one a same-length `done:` rewrite never classifies as finished.
	Digest string
}

// Reports whether c still describes this file's own history, so the bytes past c.Offset are newly
// written rather than a slice of a file that has been replaced wholesale.
func (c ReportCursor) covers(data []byte) bool {
	if c.Offset < 0 || c.Offset > int64(len(data)) {
		return false
	}
	if c.Digest != "" {
		return c.Digest == reportDigest(data[:c.Offset])
	}
	// An empty digest is a cursor persisted before hand recorded one, so the check falls back to the
	// newline boundary every persisted offset sits on: an offset whose preceding byte is not a newline
	// points into the middle of a line a longer rewrite replaced (atqamz/hand#140).
	return c.Offset == 0 || data[c.Offset-1] == '\n'
}

// The cursor that says consumed has been consumed. An empty prefix gets the zero cursor rather than
// the digest of nothing: offset 0 covers any file already, and a digest there would be a value the
// watcher persists for every task whose worker has yet to report a line.
func reportCursorFor(consumed []byte) ReportCursor {
	if len(consumed) == 0 {
		return ReportCursor{}
	}
	return ReportCursor{Offset: int64(len(consumed)), Digest: reportDigest(consumed)}
}

func reportDigest(consumed []byte) string {
	sum := sha256.Sum256(consumed)
	return hex.EncodeToString(sum[:])
}

// Reads a task's report file whole, alongside the cursor its content actually supports: the caller's
// own where that still describes this file, and a zero cursor where it does not - a file shorter than
// the offset included.
func readReport(path string, cur ReportCursor) ([]byte, ReportCursor, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, ReportCursor{}, nil
	}
	if err != nil {
		return nil, cur, fmt.Errorf("read report %s: %w", path, err)
	}
	// Re-announcing a report costs less than inventing a broken one out of a file that has been
	// replaced, or skipping one because the replacement happens to be the same size.
	if !cur.covers(data) {
		return data, ReportCursor{}, nil
	}
	return data, cur, nil
}

// TailReport reads whatever complete lines have arrived in a task's report file since cur, alongside
// the new cursor to persist. A trailing line with no terminating newline is left unconsumed in case
// the worker's append is still in flight.
func TailReport(path string, cur ReportCursor) ([]ReportLine, ReportCursor, error) {
	data, base, err := readReport(path, cur)
	if err != nil {
		return nil, base, err
	}

	tail := data[base.Offset:]
	var lines []ReportLine
	consumed := 0
	for {
		idx := bytes.IndexByte(tail[consumed:], '\n')
		if idx == -1 {
			break
		}
		line := string(tail[consumed : consumed+idx])
		consumed += idx + 1
		if !blankReportLine(line) {
			lines = append(lines, ParseReportLine(line))
		}
	}
	return lines, reportCursorFor(data[:base.Offset+int64(consumed)]), nil
}

// ReadReportLines reads and classifies every line currently in a task's report
// file, for stateless consumers like hand status that don't track an offset
// across invocations.
func ReadReportLines(homeDir, id string) ([]ReportLine, error) {
	data, err := ReadReportData(homeDir, id)
	if err != nil {
		return nil, err
	}
	return ReportLinesInData(data), nil
}

func ReadReportData(homeDir, id string) ([]byte, error) {
	data, err := os.ReadFile(ReportPath(homeDir, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read report %q: %w", id, err)
	}
	return data, nil
}

func ReportLinesInData(data []byte) []ReportLine {
	return classifyReportBytes(data)
}

// Classifies every line in data, a trailing line with no terminating newline included. That last
// line is the difference between a snapshot reader and TailReport: a watcher leaves it for its next
// tick because it will still be there, while a reader answering a question about now cannot.
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

// The skip rule both readers share, so hand status never shows an entry hand watch didn't surface.
// Whitespace-only counts as blank: otherwise a trailing "  \n" would become the last (malformed)
// report and mask a real terminal report sitting right above it.
func blankReportLine(line string) bool {
	return strings.TrimSpace(line) == ""
}

// LastReportedState returns the most recent line whose state classified - the last thing the worker
// actually said about itself - skipping trailing malformed lines rather than letting one erase it.
// Free text explains nothing, so appending some after a real report still counts (ClassifyReportLine).
func LastReportedState(lines []ReportLine) (ReportLine, bool) {
	// Taking lines rather than reading the file lets a caller that also needs the raw last line get
	// both from one read: two reads are two snapshots, and a worker appending between them would have
	// one row's raw line and its classified state describing different reports.
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

// TerminalReportPastCursor reports whether a task's report channel carries a terminal state beyond
// cur, generic over which durable cursor the caller names: report_offset/report_digest for watcher
// announcement (atqamz/hand#70), or acknowledged_offset/acknowledged_digest for acknowledgement (atqamz/hand#267).
func TerminalReportPastCursor(homeDir, id string, cur ReportCursor) (bool, error) {
	// A cursor the file's content no longer supports covers nothing, so a rewritten channel is read
	// whole - the length-preserving rewrite included, whose `done:` cur never covered (atqamz/hand#149).
	data, err := ReadReportData(homeDir, id)
	if err != nil {
		return false, err
	}
	return TerminalReportInData(data, cur), nil
}

func TerminalReportInData(data []byte, cur ReportCursor) bool {
	base := cur
	if !cur.covers(data) {
		base = ReportCursor{}
	}
	last, ok := LastReportedState(classifyReportBytes(data[base.Offset:]))
	if !ok {
		return false
	}
	return TerminalReport(last.State)
}

// CurrentReportCursor covers every complete line currently in a task's report channel, the same
// completeness rule TailReport applies to a watcher's own cursor: a trailing line with no terminating
// newline is left uncovered, in case the worker's append is still in flight.
func CurrentReportCursor(homeDir, id string) (ReportCursor, error) {
	_, cursor, err := TailReport(ReportPath(homeDir, id), ReportCursor{})
	return cursor, err
}
