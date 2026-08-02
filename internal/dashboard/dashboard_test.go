package dashboard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

const skeleton = `# Dashboard

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|

## Pending Decisions

## Recent Events

## Recent Completions

## Projects
`

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state.Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), []byte(skeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeTask(t *testing.T, home string, task state.Task) {
	t.Helper()
	if task.CreatedAt == "" {
		task.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := state.Write(home, task); err != nil {
		t.Fatal(err)
	}
}

func writeReport(t *testing.T, home, id string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(state.ReportPath(home, id), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, home string) Dashboard {
	t.Helper()
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func rendered(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseRecoversOnlyTheAppendOnlySections(t *testing.T) {
	sample := `# Dashboard

Updated: 2026-07-24T12:30:00Z

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|
| fix-login | nsr | ship | working | 2h | - |

## Pending Decisions
- dark-mode: worker needs third-party API key

## Recent Events
- 12:30 done fix-login
- 12:15 pr-merged audit-deps

## Recent Completions
- fix-typo: nsr | ship | merged | PR #40

## Projects
- nsr: direct-pr | 3 active tasks
`
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.Updated != "2026-07-24T12:30:00Z" {
		t.Fatalf("Updated = %q", d.Updated)
	}
	if len(d.RecentEvents) != 2 || len(d.RecentCompletions) != 1 {
		t.Fatalf("append-only sections = %+v", d)
	}
	if len(d.ActiveTasks) != 0 || len(d.PendingDecisions) != 0 || len(d.Projects) != 0 {
		t.Fatalf("derived sections were read back from the rendering: %+v", d)
	}
}

func TestRenderRoundTripsWhatParseRecovers(t *testing.T) {
	d := Dashboard{
		Updated:           "2026-07-24T12:30:00Z",
		RecentEvents:      []string{"12:30 done fix-login"},
		RecentCompletions: []string{"fix-typo: nsr | ship | merged | PR #40"},
	}
	back, err := Parse(Render(d))
	if err != nil {
		t.Fatal(err)
	}
	if back.Updated != d.Updated || len(back.RecentEvents) != 1 || back.RecentEvents[0] != d.RecentEvents[0] {
		t.Fatalf("round trip = %+v, want %+v", back, d)
	}
	if len(back.RecentCompletions) != 1 || back.RecentCompletions[0] != d.RecentCompletions[0] {
		t.Fatalf("completions round trip = %+v", back.RecentCompletions)
	}
}

func TestUpdateDerivesActiveTasksFromTheStore(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "fix-login", Project: "nsr", Kind: state.KindShip})
	writeReport(t, home, "fix-login", "working: on it")

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	out := rendered(t, home)
	if !strings.Contains(out, "| fix-login | nsr | ship | working |") {
		t.Fatalf("Active Tasks row missing:\n%s", out)
	}
	d := readBack(t, home)
	if _, err := time.Parse(TimeFormat, d.Updated); err != nil {
		t.Fatalf("Updated %q not in TimeFormat: %v", d.Updated, err)
	}
}

// atqamz/secondhand#53 defect 1: a task that left the store used to keep its row,
// because the previous rendering was the input to the next one.
func TestUpdateDropsTheRowOfADeletedTask(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "fix-login", Project: "nsr", Kind: state.KindShip})
	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := state.Delete(home, "fix-login"); err != nil {
		t.Fatal(err)
	}

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}
	if out := rendered(t, home); strings.Contains(out, "fix-login |") {
		t.Fatalf("row outlived the task it described:\n%s", out)
	}
}

// atqamz/secondhand#53 defect 2: a quiet pane produced `stopped, reason unknown`
// while the genuine needs-decision beside it never reached the section.
func TestPendingDecisionsHoldsOnlyWhatAWorkerReported(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "quiet", Project: "nsr", Kind: state.KindShip})
	writeTask(t, home, state.Task{ID: "asking", Project: "nsr", Kind: state.KindShip})
	writeReport(t, home, "asking", "working: started", "needs-decision: two ways to fix the race")

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	entries := pendingDecisions(t, home)
	if len(entries) != 1 {
		t.Fatalf("PendingDecisions = %+v, want exactly the reported one", entries)
	}
	if !strings.HasPrefix(entries[0], "asking: needs-decision: two ways to fix the race") {
		t.Fatalf("entry = %q", entries[0])
	}
	if !strings.Contains(entries[0], state.ReportPath(home, "asking")) {
		t.Fatalf("entry does not point at the report file: %q", entries[0])
	}
	if out := rendered(t, home); !strings.Contains(out, "| quiet | nsr | ship | "+unreported+" |") {
		t.Fatalf("unreported task not rendered as unreported:\n%s", out)
	}
}

// Parse deliberately refuses to recover a derived section, so the assertion
// reads the rendered file.
func pendingDecisions(t *testing.T, home string) []string {
	t.Helper()
	var entries []string
	inSection := false
	for _, line := range strings.Split(rendered(t, home), "\n") {
		switch {
		case line == "## "+sectionPendingDecisions:
			inSection = true
		case strings.HasPrefix(line, "## "):
			inSection = false
		case inSection && strings.HasPrefix(line, "- "):
			entries = append(entries, strings.TrimPrefix(line, "- "))
		}
	}
	return entries
}

func TestPendingDecisionsIncludesBlocked(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "stuck", Project: "nsr", Kind: state.KindShip})
	writeReport(t, home, "stuck", "blocked: needs an API key")

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	entries := pendingDecisions(t, home)
	if len(entries) != 1 || !strings.Contains(entries[0], "stuck: blocked: needs an API key") {
		t.Fatalf("PendingDecisions = %+v", entries)
	}
}

// atqamz/secondhand#53 defect 3: state `unknown` rendered next to a PR URL and a
// report file ending in `done:`.
func TestStateColumnAgreesWithTheReportBesideThePR(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{
		ID: "shipped", Project: "nsr", Kind: state.KindShip,
		PR: "https://github.com/a/b/pull/1",
	})
	writeReport(t, home, "shipped", "working: on it", "done: landed")

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	out := rendered(t, home)
	if !strings.Contains(out, "| shipped | nsr | ship | done |") {
		t.Fatalf("state column disagrees with the report:\n%s", out)
	}
	if !strings.Contains(out, "https://github.com/a/b/pull/1 |") {
		t.Fatalf("PR column missing:\n%s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Fatalf("rendered a guess:\n%s", out)
	}
}

func TestUnreadableReportCostsOnlyItsOwnStateColumn(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "broken", Project: "nsr", Kind: state.KindShip})
	writeTask(t, home, state.Task{ID: "fine", Project: "nsr", Kind: state.KindShip})
	writeReport(t, home, "fine", "working: on it")
	// A directory at the report path fails ReadFile for any uid, where a chmod
	// would not stop a test running as root.
	if err := os.Mkdir(state.ReportPath(home, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	out := rendered(t, home)
	if !strings.Contains(out, "| broken | nsr | ship | unreadable |") {
		t.Fatalf("unreadable report did not render as unreadable:\n%s", out)
	}
	if !strings.Contains(out, "| fine | nsr | ship | working |") {
		t.Fatalf("readable row lost to the unreadable one:\n%s", out)
	}
}

// atqamz/secondhand#53 defect 4: one ~200-word report line consumed the whole
// Recent Events section.
func TestLongEventIsTruncatedWithAMarker(t *testing.T) {
	home := newHome(t)
	long := strings.Repeat("x", eventBudget*2)

	if err := Update(home, UpdateOpts{AddEvent: "needs-decision task-1: " + long}); err != nil {
		t.Fatal(err)
	}

	d := readBack(t, home)
	if len(d.RecentEvents) != 1 {
		t.Fatalf("RecentEvents = %+v", d.RecentEvents)
	}
	if !strings.Contains(d.RecentEvents[0], "... [+") {
		t.Fatalf("no truncation marker: %q", d.RecentEvents[0])
	}
	if len([]rune(d.RecentEvents[0])) > eventBudget+40 {
		t.Fatalf("event line still %d runes", len([]rune(d.RecentEvents[0])))
	}
}

func TestLongPendingDecisionIsTruncatedWithAMarker(t *testing.T) {
	home := newHome(t)
	writeTask(t, home, state.Task{ID: "asking", Project: "nsr", Kind: state.KindShip})
	writeReport(t, home, "asking", "needs-decision: "+strings.Repeat("y", pendingDecisionBudget*2))

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	entries := pendingDecisions(t, home)
	if len(entries) != 1 || !strings.Contains(entries[0], "... [+") {
		t.Fatalf("PendingDecisions = %+v", entries)
	}
}

func TestUpdateDerivesProjectsWithActiveTaskCounts(t *testing.T) {
	home := newHome(t)
	for _, p := range []project.Project{
		{Name: "nsr", URL: "git@github.com:o/nsr.git", Mode: project.ModeDirectPR},
		{Name: "yes2infra", URL: "git@github.com:o/yes2infra.git", Mode: project.ModeNoMistakes},
	} {
		if err := project.Add(home, p); err != nil {
			t.Fatal(err)
		}
	}
	writeTask(t, home, state.Task{ID: "a", Project: "nsr", Kind: state.KindShip})
	writeTask(t, home, state.Task{ID: "b", Project: "nsr", Kind: state.KindScout})

	if err := Update(home, UpdateOpts{}); err != nil {
		t.Fatal(err)
	}

	out := rendered(t, home)
	if !strings.Contains(out, "- nsr: direct-pr | 2 active tasks") {
		t.Fatalf("project counts wrong:\n%s", out)
	}
	if !strings.Contains(out, "- yes2infra: no-mistakes | 0 active tasks") {
		t.Fatalf("idle project missing:\n%s", out)
	}
}

func TestUpdateAddEventBoundedTo20(t *testing.T) {
	home := newHome(t)
	for i := 0; i < 25; i++ {
		if err := Update(home, UpdateOpts{AddEvent: "done task-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	d := readBack(t, home)
	if len(d.RecentEvents) != 20 {
		t.Fatalf("RecentEvents len = %d, want 20", len(d.RecentEvents))
	}
	if !strings.Contains(d.RecentEvents[len(d.RecentEvents)-1], "task-24") {
		t.Fatalf("last event = %q, want task-24", d.RecentEvents[len(d.RecentEvents)-1])
	}
	if strings.Contains(strings.Join(d.RecentEvents, "\n"), "task-0 ") {
		t.Fatalf("oldest event should have rotated out: %v", d.RecentEvents)
	}
}

func TestUpdateCompleteBoundedTo10(t *testing.T) {
	home := newHome(t)
	for i := 0; i < 12; i++ {
		id := "task-" + strconv.Itoa(i)
		if err := Update(home, UpdateOpts{Complete: &Completion{ID: id, Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR #1"}}); err != nil {
			t.Fatal(err)
		}
	}

	d := readBack(t, home)
	if len(d.RecentCompletions) != 10 {
		t.Fatalf("RecentCompletions len = %d, want 10", len(d.RecentCompletions))
	}
	if !strings.HasPrefix(d.RecentCompletions[0], "task-2:") {
		t.Fatalf("oldest surviving completion = %q, want task-2", d.RecentCompletions[0])
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		delta time.Duration
		want  string
	}{
		{-30 * time.Second, "just now"},
		{-5 * time.Minute, "5m"},
		{-2 * time.Hour, "2h"},
		{-3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		got := FormatAge(now.Add(c.delta).Format(time.RFC3339))
		if got != c.want {
			t.Errorf("FormatAge(%v) = %q, want %q", c.delta, got, c.want)
		}
	}
	if got := FormatAge("not-a-time"); got != "unknown" {
		t.Errorf("FormatAge(invalid) = %q, want unknown", got)
	}
}

func TestUpdateMissingFileFails(t *testing.T) {
	home := t.TempDir()
	if err := Update(home, UpdateOpts{AddEvent: "done task-1"}); err == nil {
		t.Fatal("expected error for missing dashboard file")
	}
}
