package state

import (
	"os"
	"path/filepath"
	"testing"
)

// dogfoodReportLines is the literal content of a real report file captured
// from a production dogfood run (/home/atqa/handlab/state/release-dispatch.status):
// a worker that reported working/needs-decision/done the whole time while hand
// never read a single line of it. Used verbatim here instead of invented
// examples so the parser is proven against real data.
const dogfoodReportLines = `working: workflow_dispatch added to release.yaml, invoking no-mistakes
needs-decision: review gate on PR for #20 raised 2 ask-user findings - (1) concurrency group release-${{ github.ref }} does not serialize manual dispatch against push-triggered runs on main, risking concurrent release-please runs; (2) dispatch replays same release-please step that already no-op'd on issue #20, may not unblock the conflicted PR without also deleting/recreating the release branch. Run parked at review gate, run id 01KYEVGV26MD8X08MZY2VXXCSR on branch 20-release-workflow-dispatch.
done: PR https://github.com/atqamz/secondhand/pull/31 checks green
`

func TestParseReportLineAgainstDogfoodData(t *testing.T) {
	lines := splitDogfoodLines(t)

	got := ParseReportLine(lines[0])
	if got.State != ReportWorking || got.Note != "workflow_dispatch added to release.yaml, invoking no-mistakes" {
		t.Fatalf("got %+v", got)
	}

	got = ParseReportLine(lines[1])
	if got.State != ReportNeedsDecision {
		t.Fatalf("got %+v, want needs-decision", got)
	}
	wantNote := "review gate on PR for #20 raised 2 ask-user findings - (1) concurrency group release-${{ github.ref }} does not serialize manual dispatch against push-triggered runs on main, risking concurrent release-please runs; (2) dispatch replays same release-please step that already no-op'd on issue #20, may not unblock the conflicted PR without also deleting/recreating the release branch. Run parked at review gate, run id 01KYEVGV26MD8X08MZY2VXXCSR on branch 20-release-workflow-dispatch."
	if got.Note != wantNote {
		t.Fatalf("got note %q, want %q", got.Note, wantNote)
	}

	got = ParseReportLine(lines[2])
	if got.State != ReportDone || got.Note != "PR https://github.com/atqamz/secondhand/pull/31 checks green" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseReportLineRejectsUnknownPrefix(t *testing.T) {
	got := ParseReportLine("thinking: about to start")
	if !got.Malformed {
		t.Fatalf("got %+v, want malformed for an unrecognized state prefix", got)
	}
}

func TestParseReportLineRejectsMissingColon(t *testing.T) {
	got := ParseReportLine("working without a colon")
	if !got.Malformed {
		t.Fatalf("got %+v, want malformed with no colon", got)
	}
}

func TestTailReportOnceThenIncremental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-1.status")
	if err := os.WriteFile(path, []byte(dogfoodReportLines), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, offset, err := TailReport(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[2].State != ReportDone {
		t.Fatalf("got %+v, want last line done", lines[2])
	}

	lines, offset, err = TailReport(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("got %+v, want no new lines on a second tail at the same offset", lines)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("blocked: waiting on approval\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	lines, _, err = TailReport(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].State != ReportBlocked {
		t.Fatalf("got %+v, want exactly the newly appended blocked line", lines)
	}
}

func TestTailReportLeavesPartialTrailingLineUnconsumed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-1.status")
	if err := os.WriteFile(path, []byte("working: mid-flight write"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, offset, err := TailReport(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || offset != 0 {
		t.Fatalf("got lines=%+v offset=%d, want the unterminated line left unconsumed", lines, offset)
	}
}

func TestTailReportRestartsFromZeroWhenFileShrinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-1.status")
	if err := os.WriteFile(path, []byte(dogfoodReportLines), 0o644); err != nil {
		t.Fatal(err)
	}
	_, offset, err := TailReport(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("working: recreated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, err := TailReport(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Note != "recreated" {
		t.Fatalf("got %+v, want a fresh read from the start after truncation", lines)
	}
}

func TestReadReportLinesAndReportTail(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte(dogfoodReportLines), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadReportLines(home, "task-1")
	if err != nil || len(lines) != 3 || lines[2].State != ReportDone {
		t.Fatalf("got %+v err=%v, want 3 lines ending in done", lines, err)
	}

	tail, err := ReportTail(home, "task-1", 2)
	if err != nil || len(tail) != 2 || tail[0].State != ReportNeedsDecision || tail[1].State != ReportDone {
		t.Fatalf("got %+v err=%v, want the last 2 lines", tail, err)
	}
}

func TestReadReportLinesMissingFile(t *testing.T) {
	home := t.TempDir()
	lines, err := ReadReportLines(home, "task-1")
	if err != nil || len(lines) != 0 {
		t.Fatalf("got %+v err=%v, want no report and no error", lines, err)
	}
}

func splitDogfoodLines(t *testing.T) []string {
	t.Helper()
	lines := []string{}
	start := 0
	for i, r := range dogfoodReportLines {
		if r == '\n' {
			lines = append(lines, dogfoodReportLines[start:i])
			start = i + 1
		}
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	return lines
}

// A trailing free-text line must not read back as "this worker never reported":
// the live classifier keeps the last state a malformed line follows, so every
// reader recovering that state from the file has to agree with it.
func TestLastReportedStateSkipsTrailingMalformedLines(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte("needs-decision: which base branch?\nlooked at both again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadReportLines(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}

	last, ok := LastReportedState(lines)
	if !ok || last.State != ReportNeedsDecision || last.Note != "which base branch?" {
		t.Fatalf("got %+v ok=%v, want the needs-decision line", last, ok)
	}

	if raw := lines[len(lines)-1]; !raw.Malformed {
		t.Fatalf("got %+v, want the raw last line still surfaced as malformed", raw)
	}
}

func TestLastReportedStateWithOnlyMalformedLines(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte("hello\nthere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := ReadReportLines(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := LastReportedState(lines); ok {
		t.Fatalf("got ok=%v, want no reported state at all", ok)
	}
}

func TestUnacknowledgedTerminalReport(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		consumed string // the prefix a watcher has already announced
		want     bool
	}{
		{
			name:    "terminal report nobody has read",
			content: "working: on it\ndone: PR up\n",
			want:    true,
		},
		{
			name:     "terminal report a watcher already announced",
			content:  "working: on it\ndone: PR up\n",
			consumed: "working: on it\ndone: PR up\n",
		},
		{
			name:     "unread terminal report the worker has since superseded",
			content:  "done: PR up\nworking: addressing review\n",
			consumed: "",
		},
		{
			name:     "second done after a resume, the first one already announced",
			content:  "done: PR up\nworking: addressing review\ndone: review addressed\n",
			consumed: "done: PR up\nworking: addressing review\n",
			want:     true,
		},
		{
			name:    "failed counts the same as done",
			content: "failed: gate red twice\n",
			want:    true,
		},
		{
			name:    "a stop that is not terminal",
			content: "needs-decision: which base branch?\n",
		},
		{
			name:    "free text after an unread terminal report",
			content: "done: PR up\nstill tidying\n",
			want:    true,
		},
		{
			// TailReport leaves this line for the watcher's next tick, so nothing has
			// announced it - the newline must not be what decides whether a finished
			// worker is visible.
			name:    "terminal report with no terminating newline",
			content: "working: on it\ndone: PR up",
			want:    true,
		},
		{
			name:    "unterminated line the worker is still writing supersedes a done",
			content: "done: PR up\nworking: addressing rev",
		},
		{
			name: "no report file at all",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(Dir(home), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.content != "" {
				if err := os.WriteFile(ReportPath(home, "task-1"), []byte(c.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := UnacknowledgedTerminalReport(home, "task-1", int64(len(c.consumed)))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
