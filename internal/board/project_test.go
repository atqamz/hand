package board_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

const longProject = "a-project-with-a-very-long-repository-name-that-keeps-going-on"

func repoOf(name string) string {
	p, _ := filepath.Abs("/home/me/" + name)
	return p
}

func taskIn(t *testing.T, st *state.Store, project, title string) state.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Project(ctx, project); err != nil {
		if _, err := st.AddProject(ctx, project, repoOf(project), ""); err != nil {
			t.Fatal(err)
		}
	}
	task, err := st.AddTask(ctx, project, title, "")
	if err != nil {
		t.Fatal(err)
	}
	if task, err = st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	return task
}

func badge(name string) string {
	return `<span class="badge project" title="` + name + `">` + name + `</span>`
}

func twoProjects(t *testing.T) *fixture {
	t.Helper()
	fx := newFixture(t)
	ctx := context.Background()
	login := taskIn(t, fx.st, "hand", "Fix login")
	docs := taskIn(t, fx.st, "docs", "Write docs")
	taskIn(t, fx.st, longProject, "Port the importer")
	if _, err := fx.st.Ask(ctx, docs.ID, "Ship the docs today?"); err != nil {
		t.Fatal(err)
	}
	a := attempt(t, fx.st, login.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(ctx, a.ID, state.ReportProgress, "Cookie fixed"); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestTaskRowsNameTheirProjectWhenTheFleetHasSeveral(t *testing.T) {
	fx := twoProjects(t)
	tasks := region(get(t, fx.handler(), "/"), "tasks")
	contains(t, "tasks", tasks,
		`<a class="check-title" title="Fix login" href="/task/t1" target="_blank" rel="noopener">Fix login</a><span class="check-sub">`+badge("hand")+`<span class="meta">a1 codex gpt-6-luna · running</span></span>`,
		`<a class="check-title" title="Write docs" href="/task/t2" target="_blank" rel="noopener">Write docs</a><span class="check-sub">`+badge("docs")+`</span></span>`,
		badge(longProject))
	if n := strings.Count(tasks, `class="badge project"`); n != 3 {
		t.Fatalf("project badges = %d, want one per row:\n%s", n, tasks)
	}
}

func TestTaskRowsKeepTheirMarkupWithOneProject(t *testing.T) {
	fx := newFixture(t)
	active(t, fx.st, "Fix login")
	attempt(t, fx.st, 1, state.AttemptRunning, "")
	taskIn(t, fx.st, "hand", "Write docs")
	page := get(t, fx.handler(), "/")
	lacks(t, "page", page, `badge project`)
	contains(t, "tasks", region(page, "tasks"), `<span class="check-body"><a class="check-title" title="Write docs" href="/task/t2" target="_blank" rel="noopener">Write docs</a></span>`)
}

func TestTheBadgeCountsRegisteredProjectsNotProjectsWithTasks(t *testing.T) {
	fx := newFixture(t)
	taskIn(t, fx.st, "hand", "Fix login")
	if _, err := fx.st.AddProject(context.Background(), "docs", repoOf("docs"), ""); err != nil {
		t.Fatal(err)
	}
	contains(t, "tasks", region(get(t, fx.handler(), "/"), "tasks"), badge("hand"))
	contains(t, "archive", get(t, fx.handler(), "/tasks?status=active"), badge("hand"))
}

func TestNeedsNameTheProjectOfADecisionAndAReport(t *testing.T) {
	fx := twoProjects(t)
	queue := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "queue",
		queue,
		`<span class="ref">t2</span>`+badge("docs")+`<time`,
		`<span class="ref">t1</span>`+badge("hand")+`<span class="meta">`)
	if n := strings.Count(queue, `class="badge project"`); n != 2 {
		t.Fatalf("project badges in Needs = %d, want a decision and a report:\n%s", n, queue)
	}
}

func TestNeedsKeepTheirMarkupWithOneProject(t *testing.T) {
	fx := newFixture(t)
	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(context.Background(), task.ID, "Ship the docs today?"); err != nil {
		t.Fatal(err)
	}
	a := attempt(t, fx.st, task.ID, state.AttemptRunning, "")
	if _, err := fx.st.AddReport(context.Background(), a.ID, state.ReportProgress, "Cookie fixed"); err != nil {
		t.Fatal(err)
	}
	lacks(t, "queue", region(get(t, fx.handler(), "/"), "queue"), `badge project`)
}

func TestNeedsLeaveOtherKindsWithoutAProjectBadge(t *testing.T) {
	fx := newFixture(t)
	taskIn(t, fx.st, "docs", "Write docs")
	a := attempt(t, fx.st, taskIn(t, fx.st, "hand", "Fix login").ID, state.AttemptFailed, "boom")
	queue := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "queue", queue, `data-kind="failure"`, a.Reason)
	lacks(t, "queue", queue, `badge project`)
}

func TestTheArchiveNamesTheProjectWhenTheFleetHasSeveral(t *testing.T) {
	a := newArchive(t)
	a.finish(t, "hand", "Fix login", state.StatusDone, a.now)
	body := get(t, a.handler(), "/tasks")
	lacks(t, "one project", body, `badge project`, `<option value="docs"`)
	if _, err := a.st.AddProject(context.Background(), "docs", repoOf("docs"), ""); err != nil {
		t.Fatal(err)
	}
	a.finish(t, "docs", "Write docs", state.StatusDone, a.now)
	a.finish(t, "hand", "Fix search", state.StatusDone, a.now)
	body = get(t, a.handler(), "/tasks")
	contains(t, "archive", body, `<span class="check-sub">`+badge("docs")+`</span>`, `<span class="check-sub">`+badge("hand")+`</span>`, `<label class="field"><span>Project</span>`)
	if n := strings.Count(body, `class="badge project"`); n != 3 {
		t.Fatalf("project badges = %d, want one per row:\n%s", n, body)
	}
}

func TestTheLiveRegionsCarryTheProjectBadges(t *testing.T) {
	fx := twoProjects(t)
	h := quick(fx.st, fx.options)
	page := get(t, h, "/")
	ch, _ := stream(t, serve(t, h)+"/events")
	var queue, tasks string
	for _, e := range first(t, ch, 5) {
		switch e.name {
		case "queue":
			queue = e.data
		case "tasks":
			tasks = e.data
		}
	}
	for name, got := range map[string]string{"queue": queue, "tasks": tasks} {
		if got != region(page, name) || !strings.Contains(got, `class="badge project"`) {
			t.Fatalf("live %s differs from the page or lacks the badges:\n%s", name, got)
		}
	}
	if _, err := fx.st.Ask(context.Background(), 1, "Keep the cookie name?"); err != nil {
		t.Fatal(err)
	}
	live := until(t, ch, arrives("queue", "Keep the cookie name?"))
	if got := live[len(live)-1].data; strings.Count(got, `class="badge project"`) != 3 {
		t.Fatalf("a new decision lost its project badge:\n%s", got)
	}
}

func TestAProjectBadgeEllipsesALongName(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css, ".badge.project{overflow:hidden;max-width:24ch;text-overflow:ellipsis}")
}
