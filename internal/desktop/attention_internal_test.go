package desktop

import (
	"slices"
	"testing"
)

func TestSignalTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  Signal
		want string
	}{
		{"blocked with its ref", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "blocked", WorstWord: "blocked", Ref: "s1"}, "(!) Hand · golden · s1 blocked"},
		{"blocked without a ref", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "blocked", WorstWord: "blocked"}, "(!) Hand · golden · blocked"},
		{"worker with its own ref", Signal{Name: "golden", OK: true, Waiting: 2, Worst: "worker", WorstWord: "blocked", Ref: "a1"}, "(!) Hand · golden · a1 blocked"},
		{"failure", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "failure", WorstWord: "failed", Ref: "a2"}, "(!) Hand · golden · failed"},
		{"resume", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "resume", WorstWord: "interrupted"}, "(!) Hand · golden · interrupted"},
		{"fail tone without a word", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "failure"}, "(!) Hand · golden"},
		{"decision", Signal{Name: "golden", OK: true, Waiting: 3, Worst: "decision", WorstWord: "waiting"}, "(3) Hand · golden"},
		{"limited", Signal{Name: "golden", OK: true, Waiting: 1, Worst: "limited", WorstWord: "waiting"}, "(1) Hand · golden"},
		{"report only", Signal{Name: "golden", OK: true, Waiting: 2, Worst: "report", WorstWord: "waiting"}, "(2) Hand · golden"},
		{"count beats working", Signal{Name: "golden", OK: true, Waiting: 2, Worst: "decision", Working: true}, "(2) Hand · golden"},
		{"fail tone left with nothing waiting", Signal{Name: "golden", OK: true, Worst: "blocked", WorstWord: "blocked", Ref: "s1"}, "Hand · golden"},
		{"working", Signal{Name: "golden", OK: true, Working: true}, "● Hand · golden"},
		{"quiet", Signal{Name: "golden", OK: true}, "Hand · golden"},
		{"offline", Signal{Name: "golden", Waiting: 4, Worst: "blocked", WorstWord: "blocked", Ref: "s1", Working: true}, "Hand · golden · offline"},
		{"no fleet", Signal{OK: true, Waiting: 2}, "Hand"},
		{"no fleet offline", Signal{}, "Hand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sig.Title(); got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppTitleNeedsAFleetWithAName(t *testing.T) {
	model := struct{ ReadModel }{}
	for _, tc := range []struct {
		name  string
		fleet Fleet
		want  string
	}{
		{"a named fleet", Fleet{ID: "f1", Name: "alpha"}, "Hand · alpha"},
		{"an id without a name", Fleet{ID: "f1"}, "Hand"},
		{"a name without an id", Fleet{Name: "alpha"}, "Hand"},
		{"no fleet", Fleet{}, "Hand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{Model: model, Fleet: tc.fleet}
			if got := a.Title(); got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppTitleUsesTheNeedsTheWindowShows(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}, ackDone: map[string]bool{"r1": true}}
	a.Status.Needs = Needs{
		Waiting: 2, Worst: "report", WorstWord: "waiting",
		Items: []Need{{Kind: "report", Report: "r1"}, {Kind: "report", Report: "r2"}},
	}
	if got, want := a.Title(), "(1) Hand · alpha"; got != want {
		t.Fatalf("title = %q, want %q: an acknowledged report still counts", got, want)
	}
}

func TestInstallWithoutABoardPushesOffline(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}}
	var pushed []string
	a.OnTitle = func(s string) { pushed = append(pushed, s) }
	a.syncTitle()
	a.install(found{Err: ErrNoBoard}, false)
	if a.Model != nil {
		t.Fatal("the install kept a model")
	}
	want := []string{"Hand · alpha", "Hand · alpha · offline"}
	if !slices.Equal(pushed, want) {
		t.Fatalf("pushed %q, want %q", pushed, want)
	}
}
