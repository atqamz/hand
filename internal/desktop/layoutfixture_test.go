package desktop_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func loadAPI(t *testing.T, name string, out any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "board", "testdata", "api", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var env desktop.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatal(err)
	}
}

func setStatus(f *fixture, edit func(st *desktop.Status)) {
	for id, st := range f.status {
		if id == secondID {
			continue
		}
		sup := *st.Supervisor
		st.Supervisor = &sup
		edit(&st)
		f.status[id] = st
	}
}

func setTasks(f *fixture, groups ...desktop.TaskGroup) {
	f.tasks[fleetID] = desktop.Tasks{Groups: groups}
	st := f.status[fleetID]
	st.Tasks = desktop.TaskCounts{}
	for _, g := range groups {
		switch g.Name {
		case "Active":
			st.Tasks.Active = g.Count
		case "Inbox":
			st.Tasks.Inbox = g.Count
		}
	}
	f.status[fleetID] = st
}

func noNeeds(f *fixture) {
	setStatus(f, func(st *desktop.Status) { st.Needs = desktop.Needs{} })
}

func layoutReady(t *testing.T) *fixture {
	f := goldenFixture(t)
	setStatus(f, func(st *desktop.Status) {
		st.AgentState, st.Pill, st.Line = "idle", "ready", "idle"
		st.Gauge = desktop.Gauge{Text: "510K / 1M", Pct: 51}
		st.Supervisor.Pending = 0
	})
	tl := f.timeline[fleetID]
	tl.Working = nil
	f.timeline[fleetID] = tl
	return f
}

func layoutQueued(t *testing.T) *fixture {
	f := goldenFixture(t)
	setStatus(f, func(st *desktop.Status) {
		st.Gauge = desktop.Gauge{Text: "850K / 1M", Pct: 85, Level: "warn"}
		st.Supervisor.Pending = 2
	})
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{
		{Role: "operator", At: "2026-11-01T04:43:00Z", Text: "and write the summary in the PR", No: 4, Ref: "s1", Queued: true},
		{Role: "operator", At: "2026-11-01T04:42:00Z", Text: "also check the nightly pipeline", No: 3, Ref: "s1", Queued: true},
	}, tl.Entries...)
	f.timeline[fleetID] = tl
	return f
}

func layoutBlocked(t *testing.T) *fixture {
	f := goldenFixture(t)
	var st desktop.Status
	loadAPI(t, "status-blocked", &st)
	st.Gauge = desktop.Gauge{Text: "940K / 1M", Pct: 94, Level: "warn"}
	f.status[fleetID] = st
	return f
}

func layoutAsk(t *testing.T) *fixture {
	f := layoutBlocked(t)
	st := f.status[fleetID]
	sup := *st.Supervisor
	sup.Ask = &desktop.Ask{
		Question: "Which of the build folders should I clean before the next run, given that the arm64 cache and the toolchain output both live under one path?",
		Options: []desktop.AskOption{
			{Label: "1 Clean only the arm64 cache", Keys: []string{"1"}},
			{Label: "2 Clean the toolchain output as well", Keys: []string{"2"}},
			{Label: "3 Clean everything under build/ including the downloaded module cache", Keys: []string{"3"}},
			{Label: "4 Do not clean anything", Keys: []string{"4"}},
		},
	}
	st.Supervisor = &sup
	f.status[fleetID] = st
	return f
}

func layoutStopped(t *testing.T) *fixture {
	f := goldenFixture(t)
	setStatus(f, func(st *desktop.Status) {
		st.AgentState, st.Pill, st.Line = "stopped", "stopped", "stopped"
		st.Gauge = desktop.Gauge{Text: "300K / 1M", Pct: 30}
		st.Supervisor.Status, st.Supervisor.Live, st.Supervisor.Resumable = "interrupted", false, true
	})
	tl := f.timeline[fleetID]
	tl.Live, tl.Working = false, nil
	f.timeline[fleetID] = tl
	return f
}

func layoutManyTasks(t *testing.T) *fixture {
	f := goldenFixture(t)
	f.fleets[0].Name = "many-tasks-and-a-very-long-fleet-name-for-the-masthead"
	var active, inbox []desktop.TaskRow
	for i := 0; i < 30; i++ {
		r := desktop.TaskRow{
			Ref: fmt.Sprintf("t%d", 10+i), Status: "active", State: "idle", Project: "hand",
			Title: fmt.Sprintf("Task number %d: a long title that goes on and on to see how the row truncates at narrow widths", i),
		}
		if i%3 == 0 {
			r.State = "running"
			r.Attempt = &desktop.Attempt{Ref: fmt.Sprintf("a%d", 10+i), Task: r.Ref, Harness: "claude", Model: "sonnet", Status: "running"}
		}
		if i%6 == 0 {
			r.Delivered = &desktop.Deliverable{Ref: fmt.Sprintf("https://github.com/example/hand/pull/%d", 100+i), Kind: "PR"}
		}
		active = append(active, r)
	}
	for i := 0; i < 14; i++ {
		inbox = append(inbox, desktop.TaskRow{Ref: fmt.Sprintf("t%d", 50+i), Title: fmt.Sprintf("Inbox item %d", i), Status: "inbox", State: "idle", Project: "hand"})
	}
	setTasks(f, desktop.TaskGroup{Name: "Active", Count: len(active), Tasks: active}, desktop.TaskGroup{Name: "Inbox", Count: len(inbox), Tasks: inbox})
	noNeeds(f)
	return f
}

func layoutLongTask(t *testing.T) *fixture {
	f := goldenFixture(t)
	long := "Investigate why the nightly integration pipeline fails on the arm64 macOS runner with an opaque linker error after the toolchain bump and whether the cache key needs the compiler version too"
	rows := f.tasks[fleetID].Groups[0].Tasks
	rows[0].Title = long
	setTasks(f, desktop.TaskGroup{Name: "Active", Count: len(rows), Tasks: rows}, f.tasks[fleetID].Groups[1])
	st := f.status[fleetID]
	needs := st.Needs
	needs.Items = append([]desktop.Need(nil), needs.Items...)
	needs.Items[1].Title, needs.Items[1].Headline = long, long
	st.Needs = needs
	f.status[fleetID] = st
	td := f.task[fleetID]
	td.Task.Title = long
	f.task[fleetID] = td
	return f
}

func layoutDeliverables(t *testing.T) *fixture {
	f := goldenFixture(t)
	d := func(ref, kind string) *desktop.Deliverable { return &desktop.Deliverable{Ref: ref, Kind: kind} }
	rows := []desktop.TaskRow{
		{Ref: "t20", Title: "Phone screenshots for the board audit", Status: "active", State: "idle", Project: "hand",
			Delivered: d("/very/long/absolute/path/fleet-example/shots/t255/phone-390-dark-needs-blocked-after.png", "file")},
		{Ref: "t21", Title: "Fix login cookie", Status: "active", State: "running", Project: "hand", Delivered: d("https://github.com/example/hand/pull/42", "PR")},
		{Ref: "t22", Title: "Port the importer", Status: "active", State: "running", Project: "hand",
			Delivered: d("https://gitlab.example.com/group/sub/importer/-/merge_requests/17", "MR")},
		{Ref: "t23", Title: "Write the design note", Status: "active", State: "idle", Project: "hand", Delivered: d("https://docs.example.com/notes/board-design-2026-10", "link")},
		{Ref: "t24", Title: "Ship the quarterly board report with every artefact, the PR, the MR and the published note", Status: "active", State: "idle", Project: "hand",
			Delivered: d("https://github.com/example/hand/pull/43", "PR")},
		{Ref: "t25", Title: "Windows export", Status: "active", State: "idle", Project: "hand",
			Delivered: d(`C:\Users\example\Documents\reports\quarterly-board-audit-final-version-2.pdf`, "file")},
	}
	setTasks(f,
		desktop.TaskGroup{Name: "Active", Count: len(rows), Tasks: rows},
		desktop.TaskGroup{Name: "Inbox", Count: 2, Tasks: []desktop.TaskRow{
			{Ref: "t30", Title: "Inbox item without a deliverable", Status: "inbox", State: "idle", Project: "hand"},
			{Ref: "t31", Title: "Another inbox item", Status: "inbox", State: "idle", Project: "hand"},
		}})
	noNeeds(f)
	return f
}

func layoutBusy(t *testing.T) *fixture {
	f := goldenFixture(t)
	titles := []string{
		"Bump the Go toolchain to 1.26.5 and refresh the module cache key", "Port the importer to the new state API",
		"Add a render check for the board", "Write the release notes for 0.9", "Investigate the slow SSE reconnect",
		"Tidy the CLI help", "Pin luvus to 0.14.2 or track the latest minor?",
	}
	var items []desktop.Need
	for i, title := range titles {
		ref := fmt.Sprintf("t%d", 40+i)
		switch i % 3 {
		case 0:
			items = append(items, desktop.Need{Kind: "failure", Ref: fmt.Sprintf("a%d", 40+i), Title: title, Word: "FAILED", Tone: "fail", Task: ref,
				Attempt: fmt.Sprintf("a%d", 40+i), Reason: "exited 2: go vet ./... found 3 issues in internal/state",
				AttemptMeta: &desktop.NeedAttempt{Ref: fmt.Sprintf("a%d", 40+i), Harness: "claude", Model: "opus", Status: "failed", EndedAt: "2026-11-01T04:40:00Z"}})
		case 1:
			items = append(items, desktop.Need{Kind: "decision", Ref: fmt.Sprintf("d%d", 40+i), Title: title, Word: "DECISION", Tone: "wait", Task: ref,
				Decision: fmt.Sprintf("d%d", 40+i), Headline: title, Status: "open", CreatedAt: "2026-11-01T04:40:00Z"})
		default:
			items = append(items, desktop.Need{Kind: "report", Ref: fmt.Sprintf("r%d", 40+i), Title: title, Word: "REPORT", Tone: "neutral", Task: ref,
				Report: fmt.Sprintf("r%d", 40+i), Attempt: fmt.Sprintf("a%d", 40+i), Status: "done", CreatedAt: "2026-11-01T04:40:00Z",
				Excerpt: "CLI help tidied\n\nDelivered: https://github.com/example/hand/pull/51"})
		}
	}
	setStatus(f, func(st *desktop.Status) {
		st.Needs = desktop.Needs{Waiting: len(items) + 3, More: 3, Worst: "failure", WorstWord: "failed", WorstCount: 3, Items: items}
		st.Gauge = desktop.Gauge{Text: "610K / 1M", Pct: 61}
	})
	return f
}

func layoutDecisions(t *testing.T) *fixture {
	f := goldenFixture(t)
	heads := []string{
		"Pin luvus to which version?", "Merge PR 51 now?", "How should the nightly arm64 job run until the toolchain settles?",
		"What should the 0.12 release be called?", "Rotate the board token now?",
	}
	var items []desktop.Need
	for i, h := range heads {
		items = append(items, desktop.Need{Kind: "decision", Ref: fmt.Sprintf("d%d", 20+i), Title: h, Word: "DECISION", Tone: "wait", Task: fmt.Sprintf("t%d", 20+i),
			Decision: fmt.Sprintf("d%d", 20+i), Headline: h, Status: "open", CreatedAt: "2026-11-01T04:40:00Z"})
	}
	setStatus(f, func(st *desktop.Status) {
		st.Needs = desktop.Needs{Waiting: len(items), Worst: "decision", WorstWord: "decision", WorstCount: len(items), Items: items}
		st.Gauge = desktop.Gauge{Text: "420K / 1M", Pct: 42}
	})
	long := "Pin luvus to which version?\n\nCI broke when luvus 0.15 changed the output of `luvus session list`. Pick how CI follows luvus from now on.\n\n1. Pin 0.14.2 \u2014 reproducible, but misses fixes\n2. Track 0.14.x \u2014 gets fixes, can still break\n3. Other (write your own)"
	f.extra = map[string]desktop.DecisionDetail{}
	for i, h := range heads {
		ref := fmt.Sprintf("d%d", 20+i)
		dd := f.decision[fleetID]
		dd.Decision.Ref, dd.Decision.Task, dd.Decision.Headline, dd.Decision.Question = ref, fmt.Sprintf("t%d", 20+i), h, h+"\n\n1. Yes\n2. No"
		dd.Body = "1. Yes\n2. No"
		if i == 0 {
			dd.Decision.Question = long
			dd.Body = "1. Pin 0.14.2 \u2014 reproducible, but misses fixes\n2. Track 0.14.x \u2014 gets fixes, can still break\n3. Other (write your own)"
			dd.Options = []desktop.Option{
				{Key: "1", Label: "Pin 0.14.2 \u2014 reproducible, but misses fixes", Value: "1. Pin 0.14.2"},
				{Key: "2", Label: "Track 0.14.x \u2014 gets fixes, can still break", Value: "2. Track 0.14.x"},
				{Key: "3", Label: "Other (write your own)", Value: "3. Other"},
			}
		}
		f.extra[ref] = dd
	}
	return f
}

func layoutNested(t *testing.T) *fixture {
	f := goldenFixture(t)
	setTasks(f,
		desktop.TaskGroup{Name: "Active", Count: 5, Tasks: []desktop.TaskRow{
			{Ref: "t10", Title: "Ship the nightly export pipeline", Project: "hand", Status: "active", State: "running", Children: 1},
			{Ref: "t11", Title: "Write the nightly job", Project: "hand", Status: "active", State: "idle", Parent: "t10", Children: 1},
			{Ref: "t12", Title: "Retry the upload when the bucket is busy", Project: "hand", Status: "active", State: "failing", Parent: "t11",
				Attempt: &desktop.Attempt{Ref: "a12", Task: "t12", Harness: "claude", Status: "failed", Reason: "launch did not finish"}},
			{Ref: "t13", Title: "Fix login", Project: "hand", Status: "active", State: "waiting"},
			{Ref: "t14", Title: "Write the release notes", Project: "hand", Status: "active", State: "idle", Parent: "t15"},
		}},
		desktop.TaskGroup{Name: "Inbox", Count: 1, Tasks: []desktop.TaskRow{{Ref: "t16", Title: "Add dark mode toggle", Project: "hand", Status: "inbox", State: "idle"}}})
	noNeeds(f)
	return f
}

func layoutReplyFirst(t *testing.T) *fixture {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{
		{Role: "supervisor", At: "2026-11-01T04:44:00Z", Text: "Reading the brief for t4.", No: 4, Ref: "s1", Model: "opus"},
		{Role: "supervisor", At: "2026-11-01T04:43:20Z", Text: "On it: I am starting t4 now and will tell you when a4 runs.", No: 3, Ref: "s1", Model: "opus"},
		{Role: "operator", At: "2026-11-01T04:43:00Z", Text: "start t4 and tell me when it runs", No: 2, Ref: "s1"},
	}, tl.Entries...)
	tl.Working = &desktop.Working{Ref: "s1", Since: "2026-11-01T04:43:00Z", On: "since the turn began", Last: "2026-11-01T04:44:00Z"}
	f.timeline[fleetID] = tl
	return f
}

func layoutLongReply(t *testing.T) *fixture {
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{
		Role: "supervisor", At: "2026-11-01T04:50:00Z", No: 3, Ref: "s1", Model: "opus",
		Text: "Here is where the fleet stands.\n\n### Summary\n\nt3 passed review and a2 is merging. d1 is waiting on you: it decides whether the cookie keeps its old name.\n\n- t1 **Fix login** is running on a1 (claude opus high)\n- t4 is queued behind t1 because both touch `internal/board/static/board.css`\n\n```go\nfunc main() {\n\tfmt.Println(\"a very long line that should scroll horizontally rather than wrap because code blocks never wrap\")\n}\n```\n\n> Heads up: the path /very/long/path/that/never/breaks/internal/board/static/board.css:214 is unbreakable.\n\n" + strings.Repeat("Supercalifragilistic_", 8),
	}}, tl.Entries...)
	f.timeline[fleetID] = tl
	return f
}

type layoutScene struct {
	name   string
	web    string
	open   func(t *testing.T, width, height int, dark bool) *ui.Tester
	fresh  bool
	modal  func(width int) bool
	under  func(t *testing.T, width, height int, dark bool) *ui.Tester
	errors []string
	dialog int
}

func boardOpen(fx func(t *testing.T) *fixture, then func(t *testing.T, app *desktop.App, tt *ui.Tester)) func(t *testing.T, w, h int, dark bool) *ui.Tester {
	return func(t *testing.T, w, h int, dark bool) *ui.Tester {
		t.Helper()
		inUTC(t)
		app := newApp(t, fx(t))
		tt := sized(t, app, w, h, dark)
		if then != nil {
			then(t, app, tt)
		}
		return tt
	}
}

func showScreen(s desktop.Screen) func(t *testing.T, app *desktop.App, tt *ui.Tester) {
	return func(t *testing.T, app *desktop.App, tt *ui.Tester) {
		t.Helper()
		app.Show(s)
		tt.Frame()
	}
}

func openRef(ref string) func(t *testing.T, app *desktop.App, tt *ui.Tester) {
	return func(t *testing.T, app *desktop.App, tt *ui.Tester) {
		t.Helper()
		app.OpenRef(ref)
		tt.Frame()
	}
}

func layoutScenes() []layoutScene {
	return []layoutScene{
		{name: "empty", web: "e001", errors: []string{"omp not found on the board's PATH; restart the board after installing it"}, open: boardOpen(func(t *testing.T) *fixture { return emptyFixture() }, nil)},
		{name: "ready", web: "e002", fresh: true, open: boardOpen(layoutReady, nil)},
		{name: "working", web: "e003", fresh: true, open: boardOpen(layoutQueued, nil)},
		{name: "blocked", web: "e004, e016", fresh: true, open: boardOpen(layoutBlocked, nil)},
		{name: "stopped", web: "e005", fresh: true, open: boardOpen(layoutStopped, nil)},
		{name: "many-tasks", web: "e007", fresh: true, open: boardOpen(layoutManyTasks, nil)},
		{name: "long-task", web: "e00e", fresh: true, open: boardOpen(layoutLongTask, nil)},
		{name: "deliverables", web: "e00f", fresh: true, open: boardOpen(layoutDeliverables, nil)},
		{name: "busy", web: "e010", fresh: true, open: boardOpen(layoutBusy, nil)},
		{name: "decisions", web: "e011", fresh: true, open: boardOpen(layoutDecisions, nil)},
		{name: "nested-tasks", web: "e018", fresh: true, open: boardOpen(layoutNested, nil)},
		{name: "ask", web: "e013, e019", fresh: true, open: boardOpen(layoutAsk, nil)},
		{name: "reply-first", web: "e01a", fresh: true, open: boardOpen(layoutReplyFirst, nil)},
		{name: "long-reply", web: "e010 chat", fresh: true, open: boardOpen(layoutLongReply, nil)},
		{name: "tasks", web: "e010 tasks", open: boardOpen(subtaskSearchFixture, showScreen(desktop.TasksScreen))},
		{name: "task", modal: always, dialog: 95, web: "task, dialog", open: boardOpen(goldenFixture, openRef("t1"))},
		{name: "attempt", modal: always, dialog: 55, web: "attempt", open: boardOpen(goldenFixture, openRef("a1"))},
		{name: "decision", modal: always, dialog: 20, web: "decision", open: boardOpen(goldenFixture, openRef("d1"))},
		{name: "document", web: "document, documents", open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			app := newApp(t, docFixture(t))
			app.OpenDoc("reports/t1/design.md")
			return sized(t, app, w, h, dark)
		}},
		{name: "conversation", web: "log", open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			return sized(t, historyApp(t, historyModel(t)), w, h, dark)
		}},
		{name: "companion", web: "companion", open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			return sized(t, phoneApp(t, phoneFixture(t)), w, h, dark)
		}},
		{name: "alerts", web: "alerts", open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			return sized(t, alertsApp(t, withAlerts(goldenFixture(t))), w, h, dark)
		}},
		{name: "skills", web: "none, Desktop only", errors: []string{"This scope could not be read"}, open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			app, _ := skillsApp(t, goldenFixture(t))
			return sized(t, app, w, h, dark)
		}},
		{name: "overflow-menu", modal: narrow, dialog: 25, web: "menu", under: boardOpen(goldenFixture, nil), open: func(t *testing.T, w, h int, dark bool) *ui.Tester {
			t.Helper()
			tt := sized(t, newApp(t, goldenFixture(t)), w, h, dark)
			click(t, tt, menuButton(w))
			return tt
		}},
	}
}

func always(int) bool { return true }

func narrow(width int) bool { return width < 900 }

func menuButton(width int) string {
	if width < 900 {
		return "Menu"
	}
	return "More"
}
