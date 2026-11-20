package desktop_test

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func needs(waiting int, worst, word string, items ...desktop.Need) func(*desktop.Status) {
	return func(st *desktop.Status) {
		st.AgentState = ""
		st.Needs = desktop.Needs{Waiting: waiting, Worst: worst, WorstWord: word, Items: items}
	}
}

func TestTitleFollowsThePolledStatus(t *testing.T) {
	b := newFakeBoard()
	app, clock := newBoardApp(t, b)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	app.StartRefresh()
	if got := app.Title(); got != "Hand · fake" {
		t.Fatalf("title at start = %q", got)
	}
	for _, step := range []struct {
		name   string
		status func(*desktop.Status)
		want   string
	}{
		{"blocked", needs(1, "blocked", "blocked", desktop.Need{Kind: "blocked", Ref: "s1"}), "(!) Hand · fake · s1 blocked"},
		{"worker", needs(2, "worker", "blocked", desktop.Need{Kind: "worker", Ref: "a1"}), "(!) Hand · fake · a1 blocked"},
		{"failure", needs(1, "failure", "failed", desktop.Need{Kind: "failure", Ref: "a2"}), "(!) Hand · fake · failed"},
		{"resume", needs(1, "resume", "interrupted", desktop.Need{Kind: "resume", Ref: "a3"}), "(!) Hand · fake · interrupted"},
		{"decision", needs(3, "decision", "waiting", desktop.Need{Kind: "decision", Ref: "d1"}), "(3) Hand · fake"},
		{"report", needs(1, "report", "waiting", desktop.Need{Kind: "report", Ref: "r1"}), "(1) Hand · fake"},
		{"working", func(st *desktop.Status) { st.Needs = desktop.Needs{}; st.AgentState = "working" }, "● Hand · fake"},
		{"working with a need", func(st *desktop.Status) { needs(2, "decision", "waiting")(st); st.AgentState = "working" }, "(2) Hand · fake"},
		{"quiet", func(st *desktop.Status) { st.Needs = desktop.Needs{}; st.AgentState = "idle" }, "Hand · fake"},
	} {
		before := len(pushed)
		b.setStatus(step.status)
		clock.Advance(desktop.FocusedRefresh)
		if got := app.Title(); got != step.want {
			t.Fatalf("%s: title = %q, want %q", step.name, got, step.want)
		}
		if len(pushed) != before+1 || pushed[len(pushed)-1] != step.want {
			t.Fatalf("%s: pushed %q, want one more %q", step.name, pushed[before:], step.want)
		}
		for range 3 {
			clock.Advance(desktop.FocusedRefresh)
		}
		if len(pushed) != before+1 {
			t.Fatalf("%s: unchanged polls pushed %q", step.name, pushed[before+1:])
		}
	}
}

func TestTitleSaysOfflineWhileTheBoardFailsAndRecovers(t *testing.T) {
	b := newFakeBoard()
	b.setStatus(needs(1, "blocked", "blocked", desktop.Need{Kind: "blocked", Ref: "s1"}))
	app, clock := newBoardApp(t, b)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	app.StartRefresh()
	if got, want := app.Title(), "(!) Hand · fake · s1 blocked"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	b.setFail(true)
	for range 10 {
		clock.Advance(desktop.FocusedRefresh)
	}
	if want := []string{"Hand · fake · offline"}; !reflect.DeepEqual(pushed, want) {
		t.Fatalf("pushed %q while the board failed, want %q", pushed, want)
	}
	b.setFail(false)
	for i := 0; i < 80 && app.Title() == "Hand · fake · offline"; i++ {
		clock.Advance(desktop.FocusedRefresh)
	}
	if got, want := app.Title(), "(!) Hand · fake · s1 blocked"; got != want {
		t.Fatalf("title = %q after recovery, want %q", got, want)
	}
	if len(pushed) != 2 || pushed[1] != "(!) Hand · fake · s1 blocked" {
		t.Fatalf("pushed %q, want offline then the recovered title", pushed)
	}
}

func TestTitleIsTheSelectedFleetsOnly(t *testing.T) {
	f := goldenFixture(t)
	f.status[fleetID] = desktop.Status{AgentState: "idle"}
	blocked := desktop.Status{Needs: desktop.Needs{
		Waiting: 5, Worst: "blocked", WorstWord: "blocked",
		Items: []desktop.Need{{Kind: "blocked", Ref: "s9"}},
	}}
	f.status[secondID] = blocked
	app := newApp(t, f)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	if got, want := app.Title(), "Hand · golden"; got != want {
		t.Fatalf("title = %q, want %q: another fleet's need leaked in", got, want)
	}
	tt := newTester(app)
	click(t, tt, "second")
	if got, want := app.Title(), "(!) Hand · second · s9 blocked"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if len(pushed) == 0 || pushed[len(pushed)-1] != "(!) Hand · second · s9 blocked" {
		t.Fatalf("pushed %q, want the second fleet's title last", pushed)
	}
	click(t, tt, "golden")
	if got, want := app.Title(), "Hand · golden"; got != want {
		t.Fatalf("title = %q after switching back, want %q", got, want)
	}
}

func TestTitleIsPlainWithoutAFleet(t *testing.T) {
	app := newLandingApp(t, noFleetsFixture())
	if got := app.Title(); got != "Hand" {
		t.Fatalf("title = %q, want Hand", got)
	}
}

func TestViewNeverReadsTheBoardOrSetsTheTitle(t *testing.T) {
	b := newFakeBoard()
	b.setStatus(needs(1, "blocked", "blocked", desktop.Need{Kind: "blocked", Ref: "s1"}))
	app, _ := newBoardApp(t, b)
	pushes := 0
	app.OnTitle = func(string) { pushes++ }
	tt := newTester(app)
	before := b.mark()
	for range 200 {
		tt.Frame()
	}
	if got := b.mark() - before; got != 0 {
		t.Fatalf("200 frames made %d requests, want 0", got)
	}
	if pushes != 0 {
		t.Fatalf("200 frames set the title %d times, want 0", pushes)
	}
}

func quietNeeds(t *testing.T, f *fixture, kinds ...string) []desktop.Need {
	t.Helper()
	st := f.status[fleetID]
	var items []desktop.Need
	for _, n := range st.Needs.Items {
		if slices.Contains(kinds, n.Kind) {
			items = append(items, n)
		}
	}
	if len(items) == 0 {
		t.Fatalf("the fixture has no %v need", kinds)
	}
	st.AgentState = "idle"
	st.Needs = desktop.Needs{Waiting: len(items), Worst: items[0].Kind, WorstWord: "waiting", Items: items}
	f.status[fleetID] = st
	return items
}

func countTitle(n int) string { return "(" + strconv.Itoa(n) + ") Hand · golden" }

func TestTitleDropsAnAcknowledgedReportBeforeTheNextRead(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	items := quietNeeds(t, f, "report")
	app := newApp(t, f)
	if got, want := app.Title(), countTitle(len(items)); got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	f.fail = errors.New("the status read failed")
	app.Ack("r1")
	if want := countTitle(len(items) - 1); app.Title() != want || !slices.Equal(pushed, []string{want}) {
		t.Fatalf("title = %q, pushed %q, want %q", app.Title(), pushed, want)
	}
}

func TestTitleFollowsAStatusAControlReturns(t *testing.T) {
	f := goldenFixture(t)
	items := quietNeeds(t, f, "decision", "report")
	app := newApp(t, f)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	tt := newTester(app)
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	if want := countTitle(len(items) - 1); app.Title() != want || len(pushed) == 0 || pushed[len(pushed)-1] != want {
		t.Fatalf("title = %q, pushed %q, want %q", app.Title(), pushed, want)
	}
}

func TestTitleChangesWithTheFleetBeforeItsStatusLoads(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.SelectFleet(secondID)
	if want := []string{"Hand · second"}; !slices.Equal(pushed, want) {
		t.Fatalf("pushed %q before the load landed, want %q", pushed, want)
	}
}

func TestTitleSaysOfflineWhenALoadFails(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	var pushed []string
	app.OnTitle = func(s string) { pushed = append(pushed, s) }
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	app.Reload()
	if want := []string{"Hand · golden · offline"}; !slices.Equal(pushed, want) {
		t.Fatalf("pushed %q after a failed load, want %q", pushed, want)
	}
}
