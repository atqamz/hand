package state

import (
	"os"
	"path/filepath"
	"testing"
)

// Literal content of a report file captured from a dogfood run
// (/home/atqa/handlab/state/release-dispatch.status), where a worker reported the whole
// time and hand read nothing. Verbatim, not invented, so the parser meets real data.
const dogfoodReportLines = `working: workflow_dispatch added to release.yaml, invoking no-mistakes
needs-decision: review gate on PR for #20 raised 2 ask-user findings - (1) concurrency group release-${{ github.ref }} does not serialize manual dispatch against push-triggered runs on main, risking concurrent release-please runs; (2) dispatch replays same release-please step that already no-op'd on issue #20, may not unblock the conflicted PR without also deleting/recreating the release branch. Run parked at review gate, run id 01KYEVGV26MD8X08MZY2VXXCSR on branch 20-release-workflow-dispatch.
done: PR https://github.com/atqamz/hand/pull/31 checks green
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
	if got.State != ReportDone || got.Note != "PR https://github.com/atqamz/hand/pull/31 checks green" {
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

	lines, cursor, err := TailReport(path, ReportCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[2].State != ReportDone {
		t.Fatalf("got %+v, want last line done", lines[2])
	}

	lines, cursor, err = TailReport(path, cursor)
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

	lines, _, err = TailReport(path, cursor)
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

	lines, cursor, err := TailReport(path, ReportCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || cursor != (ReportCursor{}) {
		t.Fatalf("got lines=%+v cursor=%+v, want the unterminated line left unconsumed", lines, cursor)
	}
}

func TestTailReportRestartsFromZeroWhenFileShrinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-1.status")
	if err := os.WriteFile(path, []byte(dogfoodReportLines), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cursor, err := TailReport(path, ReportCursor{})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("working: recreated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, err := TailReport(path, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Note != "recreated" {
		t.Fatalf("got %+v, want a fresh read from the start after truncation", lines)
	}
}

// Three reports captured off this fleet's live report files, each rewritten in place over a
// shorter consumed one. All were announced as "malformed report" with a mid-word fragment of
// themselves (atqamz/hand#140): the consumed offset survived the rewrite, mid-line.
func TestTailReportAfterInPlaceRewrite(t *testing.T) {
	cases := []struct {
		name     string
		consumed string
		rewrite  string
		state    string
		note     string
	}{
		{
			name:     "paused report carrying a PR URL",
			consumed: "paused: gate slot requested, PR opening next\n",
			rewrite:  "paused: PR https://github.com/atqamz/hand/pull/139 open and ready, waiting on gate slot go\n",
			state:    ReportPaused,
			note:     "PR https://github.com/atqamz/hand/pull/139 open and ready, waiting on gate slot go",
		},
		{
			name:     "working report carrying owner:branch",
			consumed: "working: reading the ghutil call sites for --head\n",
			rewrite:  "working: gh confirmed --head takes plain branch name (qualified owner:branch returns nothing); implementing multi-repo search in ghutil\n",
			state:    ReportWorking,
			note:     "gh confirmed --head takes plain branch name (qualified owner:branch returns nothing); implementing multi-repo search in ghutil",
		},
		{
			name:     "working report with no second colon at all",
			consumed: "working: mapping the parked latch across watcher and store\n",
			rewrite:  "working: adding durable pane_started_at and parked_fired_for columns to internal/store\n",
			state:    ReportWorking,
			note:     "adding durable pane_started_at and parked_fired_for columns to internal/store",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task-1.status")
			if err := os.WriteFile(path, []byte(c.consumed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, cursor, err := TailReport(path, ReportCursor{})
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(c.rewrite), 0o644); err != nil {
				t.Fatal(err)
			}
			lines, cursor, err := TailReport(path, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 {
				t.Fatalf("got %+v, want exactly the rewritten report", lines)
			}
			if lines[0].Malformed || lines[0].State != c.state || lines[0].Note != c.note {
				t.Fatalf("got %+v, want state %q note %q", lines[0], c.state, c.note)
			}
			if cursor.Offset != int64(len(c.rewrite)) {
				t.Fatalf("got offset %d, want %d", cursor.Offset, len(c.rewrite))
			}

			lines, _, err = TailReport(path, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 0 {
				t.Fatalf("got %+v, want the rewritten report announced once", lines)
			}
		})
	}
}

// The same rewrite must not cost a silent completion either: a done report read
// from a stale offset is a fragment, and a fragment classifies as nothing.
func TestTerminalReportPastCursorAfterInPlaceRewrite(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	consumed := "working: gate green, merging\n"
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte(consumed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte("done: PR https://github.com/atqamz/hand/pull/139 merged and issue closed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := TerminalReportPastCursor(home, "task-1", reportCursorFor([]byte(consumed)))
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("got false, want the rewritten done report still unacknowledged")
	}
}

// The rewrite that carries no length change at all: reports are one line of house-style prose, so two
// consecutive ones landing on the same byte count is a matter of time, and every quantity the offset
// has to offer is identical across it - so the new report was skipped entirely (atqamz/hand#149).
func TestTailReportAfterSameLengthInPlaceRewrite(t *testing.T) {
	cases := []struct {
		name     string
		consumed string
		rewrite  string
		state    string
		note     string
	}{
		{
			name:     "working over working",
			consumed: "working: same-length rewrite now reproduced in the tests\n",
			rewrite:  "working: rebasing onto main to read the code that exists\n",
			state:    ReportWorking,
			note:     "rebasing onto main to read the code that exists",
		},
		{
			// The one that costs a completion rather than a wake: skipped here, the
			// last report state stays `working` and ClassifyDeferredDone never runs.
			name:     "done over working",
			consumed: "working: gate green, merge in flight\n",
			rewrite:  "done: PR 149 merged and issue closed\n",
			state:    ReportDone,
			note:     "PR 149 merged and issue closed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.consumed) != len(c.rewrite) {
				t.Fatalf("consumed %d bytes and rewrite %d bytes, want the collision this case exists for", len(c.consumed), len(c.rewrite))
			}
			path := filepath.Join(t.TempDir(), "task-1.status")
			if err := os.WriteFile(path, []byte(c.consumed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, cursor, err := TailReport(path, ReportCursor{})
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(c.rewrite), 0o644); err != nil {
				t.Fatal(err)
			}
			lines, cursor, err := TailReport(path, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 {
				t.Fatalf("got %+v, want exactly the rewritten report", lines)
			}
			if lines[0].Malformed || lines[0].State != c.state || lines[0].Note != c.note {
				t.Fatalf("got %+v, want state %q note %q", lines[0], c.state, c.note)
			}

			lines, _, err = TailReport(path, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 0 {
				t.Fatalf("got %+v, want the rewritten report announced once", lines)
			}
		})
	}
}

// A same-length rewrite is the silent completion reached by its own path: with
// nothing past the offset, hand status called the finished worker acknowledged
// too.
func TestTerminalReportPastCursorAfterSameLengthRewrite(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	consumed := "working: gate green, merge in flight\n"
	rewrite := "done: PR 149 merged and issue closed\n"
	if len(consumed) != len(rewrite) {
		t.Fatalf("consumed %d bytes and rewrite %d bytes, want the collision this test exists for", len(consumed), len(rewrite))
	}
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte(rewrite), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := TerminalReportPastCursor(home, "task-1", reportCursorFor([]byte(consumed)))
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("got false, want the same-length done rewrite still unacknowledged")
	}
}

// A cursor from a row written before report_digest existed keeps the guard it was written under: the
// same-length rewrite is missed exactly as before, and the longer one atqamz/hand#140 covers is
// still caught. The alternative is replaying every consumed line on the tick after an upgrade.
func TestTailReportFallsBackToTheNewlineBoundaryWithoutADigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-1.status")
	consumed := "working: same-length rewrite now reproduced in the tests\n"
	if err := os.WriteFile(path, []byte("working: rebasing onto main to read the code that exists\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, cursor, err := TailReport(path, ReportCursor{Offset: int64(len(consumed))})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("got %+v, want a digestless cursor to keep trusting its offset", lines)
	}
	if cursor.Digest == "" {
		t.Fatal("got no digest, want the cursor upgraded so the next rewrite is caught")
	}
}

func TestReadReportLines(t *testing.T) {
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

func TestTerminalReportPastCursor(t *testing.T) {
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

			got, err := TerminalReportPastCursor(home, "task-1", reportCursorFor([]byte(c.consumed)))
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestTerminalReportInData(t *testing.T) {
	data := []byte("working: on it\ndone: PR up\n")
	if !TerminalReportInData(data, ReportCursor{}) {
		t.Fatal("got false, want terminal report")
	}
	cursor := reportCursorFor(data)
	if TerminalReportInData(data, cursor) {
		t.Fatal("got true, want covered terminal report")
	}
	if !TerminalReportInData(data, reportCursorFor([]byte("working: on it\n"))) {
		t.Fatal("got false, want terminal report after cursor")
	}
}

// hand ack takes this cursor as everything a supervisor is acknowledging right now, so it must cover
// exactly what TailReport would announce - the trailing unterminated line included in neither.
func TestCurrentReportCursorCoversOnlyCompleteLines(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "working: on it\ndone: PR up\nstill tidying"
	if err := os.WriteFile(ReportPath(home, "task-1"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cursor, err := CurrentReportCursor(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "working: on it\ndone: PR up\n"
	if cursor.Offset != int64(len(want)) {
		t.Fatalf("Offset = %d, want %d (up to the last complete line)", cursor.Offset, len(want))
	}
	if cursor.Digest != reportDigest([]byte(want)) {
		t.Fatal("Digest does not match the complete-lines prefix")
	}

	got, err := TerminalReportPastCursor(home, "task-1", cursor)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("got true, want the acknowledged done report covered by its own cursor")
	}
}

func TestCurrentReportCursorMissingFile(t *testing.T) {
	home := t.TempDir()
	cursor, err := CurrentReportCursor(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if cursor != (ReportCursor{}) {
		t.Fatalf("cursor = %+v, want the zero cursor for a report that does not exist", cursor)
	}
}
