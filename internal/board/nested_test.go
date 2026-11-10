package board_test

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
	_ "modernc.org/sqlite"
)

func under(t *testing.T, st *state.Store, parent int64, title string) state.Task {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Project(ctx, "hand"); err != nil {
		if _, err := st.AddProject(ctx, "hand", handRepo, ""); err != nil {
			t.Fatal(err)
		}
	}
	task, err := st.AddTaskUnder(ctx, parent, "hand", title, "")
	if err != nil {
		t.Fatal(err)
	}
	if task, err = st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	return task
}

func finish(t *testing.T, st *state.Store, id int64) {
	t.Helper()
	if _, err := st.Transition(context.Background(), id, state.StatusDone); err != nil {
		t.Fatal(err)
	}
}

var rowRef = regexp.MustCompile(`<li class="row check task-row([^"]*)" data-state="[a-z]+" data-tone="[a-z]+">[\s\S]*?href="[^"]*/task/(t[0-9]+)"`)

func order(body string) []string {
	var out []string
	for _, m := range rowRef.FindAllStringSubmatch(region(body, "tasks"), -1) {
		out = append(out, strings.TrimSpace(m[2]+" "+strings.TrimSpace(m[1])))
	}
	return out
}

func TestNestedCardsFollowTheirParentAndIndentByDepthClass(t *testing.T) {
	st := open(t)
	root := active(t, st, "Root")
	other := active(t, st, "Other root")
	d1 := under(t, st, root.ID, "Level one")
	d2 := under(t, st, d1.ID, "Level two")
	d3 := under(t, st, d2.ID, "Level three")
	d4 := under(t, st, d3.ID, "Level four")
	under(t, st, d4.ID, "Level five")
	under(t, st, root.ID, "Second child")
	h := board.New(st, token, board.Options{})
	body := get(t, h, "/")
	got := strings.Join(order(body), " ")
	want := "t1 t3 depth-1 t4 depth-2 t5 depth-3 t6 depth-3 t7 depth-3 t8 depth-1 t2"
	if got != want {
		t.Fatalf("order = %q, want %q (other = %s)", got, want, state.TaskRef(other.ID))
	}
	tasks := region(body, "tasks")
	contains(t, "tasks", tasks, `<span class="meta depth-mark">depth 4</span>`, `<span class="meta depth-mark">depth 5</span>`, ">2 sub-tasks</a>", ">1 sub-task</a>")
	if n := strings.Count(tasks, "depth-mark"); n != 2 {
		t.Fatalf("depth markers = %d, want 2 (depth 4 and 5 only)", n)
	}
	lacks(t, "tasks", tasks, "depth-4", "depth-5", "under t")
	lacks(t, "page", body, ` style=`, "<style")
	if n := len(regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1)); n != 1 {
		t.Fatalf("scripts = %d, want the one external app.js", n)
	}
	contains(t, "parent card", checks(body)["t1"], `<a class="meta rel" href="/task/t1" target="_blank" rel="noopener">2 sub-tasks</a>`)
}

func TestNestedChildInAnotherGroupIsARootWithAnUnderLink(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	activeParent := active(t, st, "Active parent")
	inboxKid, err := st.AddTaskUnder(ctx, activeParent.ID, "hand", "Inbox child", "")
	if err != nil {
		t.Fatal(err)
	}
	doneParent := active(t, st, "Done parent")
	finish(t, st, doneParent.ID)
	under(t, st, doneParent.ID, "Active under done")
	doneKid := under(t, st, activeParent.ID, "Done child")
	finish(t, st, doneKid.ID)
	h := board.New(st, token, board.Options{})
	body := get(t, h, "/")
	rows := checks(body)
	contains(t, "inbox child", rows[state.TaskRef(inboxKid.ID)], `>under t1</a>`)
	contains(t, "active under done", rows["t4"], `>under t3</a>`)
	contains(t, "done child", rows["t5"], `>under t1</a>`)
	contains(t, "parent", rows["t1"], ">2 sub-tasks</a>")
	contains(t, "done parent", rows["t3"], ">1 sub-task</a>")
	if got := strings.Join(order(body), " "); got != "t1 t4 t2 t5 t3" {
		t.Fatalf("order = %q", got)
	}
	lacks(t, "rows", strings.Join(order(body), " "), "depth-")
}

func TestNestedChildWhoseParentIsOffThePageIsARoot(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	kid := active(t, st, "Child")
	for range 499 {
		if _, err := st.AddTask(ctx, "hand", "Filler", ""); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := st.AddTask(ctx, "hand", "Parent past the page", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetParent(ctx, kid.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	rows := checks(get(t, h, "/"))
	if _, ok := rows[state.TaskRef(parent.ID)]; ok {
		t.Fatal("the parent is on the page")
	}
	contains(t, "child", rows["t1"], `>under `+state.TaskRef(parent.ID)+`</a>`)
}

func TestNestedChildWhoseParentLeftTheFinishedRailIsARoot(t *testing.T) {
	st := open(t)
	parent := active(t, st, "Old parent")
	finish(t, st, parent.ID)
	for range 5 {
		finish(t, st, active(t, st, "Newer").ID)
	}
	kid := under(t, st, parent.ID, "Child")
	h := board.New(st, token, board.Options{})
	rows := checks(get(t, h, "/"))
	if _, ok := rows["t1"]; ok {
		t.Fatal("the old parent is on the rail")
	}
	contains(t, "child", rows[state.TaskRef(kid.ID)], `>under t1</a>`)
}

func TestNestedTaskPageNamesTheParentAndListsChildren(t *testing.T) {
	st := open(t)
	parent := active(t, st, "Parent")
	one := under(t, st, parent.ID, "First child")
	two := under(t, st, parent.ID, "Second child")
	finish(t, st, two.ID)
	under(t, st, one.ID, "Grandchild")
	h := board.New(st, token, board.Options{})

	page := get(t, h, "/task/t1")
	contains(t, "parent page", page, "<h3>Sub-tasks</h3>", `href="/task/t2">t2</a>`, "First child", "Second child", `data-state="passing"`)
	lacks(t, "parent page", page, "Sub-task of", "Grandchild")

	kid := get(t, h, "/task/t2")
	contains(t, "child page", kid, `Sub-task of <a class="ref" href="/task/t1">t1</a>`, "Grandchild")

	root := get(t, h, "/task/t4")
	contains(t, "grandchild page", root, `Sub-task of <a class="ref" href="/task/t2">t2</a>`)
	lacks(t, "grandchild page", root, "<h3>Sub-tasks</h3>")
}

func TestNestedArchiveStaysFlat(t *testing.T) {
	st := open(t)
	parent := active(t, st, "Parent")
	kid := under(t, st, parent.ID, "Child")
	finish(t, st, kid.ID)
	finish(t, st, parent.ID)
	h := board.New(st, token, board.Options{})
	body := get(t, h, "/tasks?status=done")
	contains(t, "archive", body, "Parent", "Child")
	lacks(t, "archive", body, "depth-", "sub-task", "under t")
}

func TestNestedAPICarriesParentAndChildren(t *testing.T) {
	st := open(t)
	parent := active(t, st, "Parent")
	kid := under(t, st, parent.ID, "Child")
	under(t, st, kid.ID, "Grandchild")
	finish(t, st, active(t, st, "Plain").ID)
	h := board.New(st, token, board.Options{Controls: true})

	_, tasks := apiInto[board.APITasks](t, h, "/api/v1/tasks")
	rows := map[string]board.APITaskRow{}
	for _, g := range tasks.Groups {
		for _, r := range g.Tasks {
			rows[r.Ref] = r
		}
	}
	if r := rows["t1"]; r.Parent != "" || r.Children != 1 {
		t.Fatalf("t1 = %+v", r)
	}
	if r := rows["t2"]; r.Parent != "t1" || r.Children != 1 {
		t.Fatalf("t2 = %+v", r)
	}
	if r := rows["t3"]; r.Parent != "t2" || r.Children != 0 {
		t.Fatalf("t3 = %+v", r)
	}

	_, detail := apiInto[board.APITaskDetail](t, h, "/api/v1/task/t1")
	if detail.Task.Parent != "" || len(detail.Children) != 1 || detail.Children[0].Ref != "t2" || detail.Children[0].Parent != "t1" {
		t.Fatalf("t1 detail = %+v", detail)
	}
	_, detail = apiInto[board.APITaskDetail](t, h, "/api/v1/task/t3")
	if detail.Task.Parent != "t2" || len(detail.Children) != 0 {
		t.Fatalf("t3 detail = %+v", detail)
	}
}

func TestAPIWithoutNestingHasNoParentOrChildren(t *testing.T) {
	st := open(t)
	active(t, st, "One")
	finish(t, st, active(t, st, "Two").ID)
	h := board.New(st, token, board.Options{Controls: true})
	for _, path := range []string{"/api/v1/tasks", "/api/v1/task/t1"} {
		rec := request(h, "GET", path, nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		lacks(t, path, rec.Body.String(), `"parent"`, `"children"`)
	}
}

func TestNestedDanglingParentIsAPlainRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	st, err := state.Open(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	parent := active(t, st, "Parent")
	kid := under(t, st, parent.ID, "Orphan")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE task SET parent_id = 999 WHERE id = ?`, kid.ID); err != nil {
		t.Fatal(err)
	}
	rows := checks(get(t, board.New(st, token, board.Options{}), "/"))
	lacks(t, "orphan", rows[state.TaskRef(kid.ID)], "under t")
	lacks(t, "orphan", rows[state.TaskRef(kid.ID)], "/task/t999")
}
