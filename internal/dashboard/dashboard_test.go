package dashboard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func writeSkeleton(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dashboard.md")
	if err := os.WriteFile(path, []byte(skeleton), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRenderRoundTrip(t *testing.T) {
	sample := `# Dashboard

Updated: 2026-07-24T12:30:00Z

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|
| fix-login | nsr | ship | working | 2h | - |
| dark-mode | nsr | ship | blocked | 45m | #43 |

## Pending Decisions
- dark-mode: worker needs third-party API key (blocked 45m)

## Recent Events
- 12:30 done fix-login
- 12:15 pr-merged audit-deps: https://github.com/yes2games/nsr/pull/40
- 11:45 blocked dark-mode: needs API key for third-party service

## Recent Completions
- fix-typo: nsr | ship | merged | PR #40
- audit-deps: nsr | scout | done | report data/audit-deps/report.md

## Projects
- nsr: direct-pr | 3 active tasks
- yes2infra: no-mistakes | 0 active tasks
`
	d, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.Updated != "2026-07-24T12:30:00Z" {
		t.Fatalf("Updated = %q", d.Updated)
	}
	if len(d.ActiveTasks) != 2 || d.ActiveTasks[1].PR != "#43" || d.ActiveTasks[0].PR != "" {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
	}
	if len(d.PendingDecisions) != 1 || len(d.RecentEvents) != 3 || len(d.RecentCompletions) != 2 {
		t.Fatalf("d = %+v", d)
	}
	if len(d.Projects) != 2 || d.Projects[0].Name != "nsr" || d.Projects[0].ActiveTaskCount != 3 {
		t.Fatalf("Projects = %+v", d.Projects)
	}

	rendered := string(Render(d))
	if rendered != sample {
		t.Fatalf("round trip mismatch:\ngot:\n%s\nwant:\n%s", rendered, sample)
	}
}

func TestUpdateAddActiveTask(t *testing.T) {
	path := writeSkeleton(t)

	if err := Update(path, UpdateOpts{AddActiveTask: &ActiveTask{
		ID: "fix-login", Project: "nsr", Kind: "ship", State: "working", Age: "just now",
	}}); err != nil {
		t.Fatal(err)
	}

	d := readBack(t, path)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].ID != "fix-login" || d.ActiveTasks[0].PR != "" {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
	}
	if d.Updated == "" {
		t.Fatal("Updated was not stamped")
	}
	if _, err := time.Parse(TimeFormat, d.Updated); err != nil {
		t.Fatalf("Updated %q not in TimeFormat: %v", d.Updated, err)
	}
}

func TestUpdateAgentState(t *testing.T) {
	path := writeSkeleton(t)
	if err := Update(path, UpdateOpts{AddActiveTask: &ActiveTask{ID: "task-1", Project: "nsr", Kind: "ship", State: "working", Age: "just now"}}); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, UpdateOpts{UpdateAgentState: &AgentStateUpdate{ID: "task-1", State: "blocked", Age: "5m"}}); err != nil {
		t.Fatal(err)
	}

	d := readBack(t, path)
	if len(d.ActiveTasks) != 1 || d.ActiveTasks[0].State != "blocked" || d.ActiveTasks[0].Age != "5m" {
		t.Fatalf("ActiveTasks = %+v", d.ActiveTasks)
	}
}

func TestUpdateAddEventBoundedTo20(t *testing.T) {
	path := writeSkeleton(t)
	for i := 0; i < 25; i++ {
		if err := Update(path, UpdateOpts{AddEvent: "done task-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	d := readBack(t, path)
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

func TestUpdateCompleteMovesTaskToCompletions(t *testing.T) {
	path := writeSkeleton(t)
	if err := Update(path, UpdateOpts{AddActiveTask: &ActiveTask{ID: "fix-login", Project: "nsr", Kind: "ship", State: "working", Age: "2h"}}); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, UpdateOpts{Complete: &Completion{
		ID: "fix-login", Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR #40",
	}}); err != nil {
		t.Fatal(err)
	}

	d := readBack(t, path)
	if len(d.ActiveTasks) != 0 {
		t.Fatalf("ActiveTasks = %+v, want empty", d.ActiveTasks)
	}
	if len(d.RecentCompletions) != 1 || d.RecentCompletions[0] != "fix-login: nsr | ship | merged | PR #40" {
		t.Fatalf("RecentCompletions = %+v", d.RecentCompletions)
	}
}

func TestUpdateCompleteBoundedTo10(t *testing.T) {
	path := writeSkeleton(t)
	for i := 0; i < 12; i++ {
		id := "task-" + strconv.Itoa(i)
		if err := Update(path, UpdateOpts{Complete: &Completion{ID: id, Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR #1"}}); err != nil {
			t.Fatal(err)
		}
	}

	d := readBack(t, path)
	if len(d.RecentCompletions) != 10 {
		t.Fatalf("RecentCompletions len = %d, want 10", len(d.RecentCompletions))
	}
	if !strings.HasPrefix(d.RecentCompletions[0], "task-2:") {
		t.Fatalf("oldest surviving completion = %q, want task-2", d.RecentCompletions[0])
	}
}

func TestUpdatePendingDecisionUpsertAndClear(t *testing.T) {
	path := writeSkeleton(t)

	if err := Update(path, UpdateOpts{SetPendingDecision: &PendingDecision{ID: "dark-mode", Text: "needs API key (blocked 5m)"}}); err != nil {
		t.Fatal(err)
	}
	d := readBack(t, path)
	if len(d.PendingDecisions) != 1 || d.PendingDecisions[0] != "dark-mode: needs API key (blocked 5m)" {
		t.Fatalf("PendingDecisions = %+v", d.PendingDecisions)
	}

	if err := Update(path, UpdateOpts{SetPendingDecision: &PendingDecision{ID: "dark-mode", Text: "needs API key (blocked 45m)"}}); err != nil {
		t.Fatal(err)
	}
	d = readBack(t, path)
	if len(d.PendingDecisions) != 1 || d.PendingDecisions[0] != "dark-mode: needs API key (blocked 45m)" {
		t.Fatalf("PendingDecisions after upsert = %+v", d.PendingDecisions)
	}

	if err := Update(path, UpdateOpts{ClearPendingDecision: "dark-mode"}); err != nil {
		t.Fatal(err)
	}
	d = readBack(t, path)
	if len(d.PendingDecisions) != 0 {
		t.Fatalf("PendingDecisions after clear = %+v", d.PendingDecisions)
	}
}

func TestUpdateSetProjects(t *testing.T) {
	path := writeSkeleton(t)

	if err := Update(path, UpdateOpts{SetProjects: []ProjectSummary{
		{Name: "nsr", Mode: "direct-pr", ActiveTaskCount: 3},
		{Name: "yes2infra", Mode: "no-mistakes", ActiveTaskCount: 0},
	}}); err != nil {
		t.Fatal(err)
	}

	d := readBack(t, path)
	if len(d.Projects) != 2 || d.Projects[0].ActiveTaskCount != 3 || d.Projects[1].Name != "yes2infra" {
		t.Fatalf("Projects = %+v", d.Projects)
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
	path := filepath.Join(t.TempDir(), "missing.md")
	if err := Update(path, UpdateOpts{AddEvent: "done task-1"}); err == nil {
		t.Fatal("expected error for missing dashboard file")
	}
}

func readBack(t *testing.T, path string) Dashboard {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
