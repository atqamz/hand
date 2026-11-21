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

func TestAppTitleNamesTheWorstNeedsRefNotTheFirstItems(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}}
	a.Status.Needs = Needs{
		Waiting: 2, Worst: "blocked", WorstWord: "blocked",
		Items: []Need{{Kind: "decision", Ref: "d1"}, {Kind: "blocked", Ref: "s1"}},
	}
	if got, want := a.Title(), "(!) Hand · alpha · s1 blocked"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestInstallWithoutABoardPushesOffline(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}}
	var pushed []string
	a.OnTitle = func(s string) { pushed = append(pushed, s) }
	a.syncAttention()
	a.install(found{Err: ErrNoBoard}, false)
	if a.Model != nil {
		t.Fatal("the install kept a model")
	}
	want := []string{"Hand · alpha", "Hand · alpha · offline"}
	if !slices.Equal(pushed, want) {
		t.Fatalf("pushed %q, want %q", pushed, want)
	}
}

func TestSignalMark(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sig          Signal
		tone, status string
	}{
		{"blocked", Signal{OK: true, Waiting: 1, Worst: "blocked", WorstWord: "blocked"}, "fail", "blocked"},
		{"worker", Signal{OK: true, Waiting: 1, Worst: "worker", WorstWord: "blocked"}, "fail", "blocked"},
		{"failure", Signal{OK: true, Waiting: 1, Worst: "failure", WorstWord: "failed"}, "fail", "failed"},
		{"resume", Signal{OK: true, Waiting: 1, Worst: "resume", WorstWord: "interrupted"}, "fail", "interrupted"},
		{"fail without a word", Signal{OK: true, Waiting: 1, Worst: "failure"}, "fail", "waiting"},
		{"decision", Signal{OK: true, Waiting: 2, Worst: "decision", WorstWord: "waiting"}, "wait", "waiting"},
		{"limited", Signal{OK: true, Waiting: 1, Worst: "limited", WorstWord: "waiting"}, "wait", "waiting"},
		{"nosup", Signal{OK: true, Waiting: 1, Worst: "nosup", WorstWord: "waiting"}, "wait", "waiting"},
		{"stalled", Signal{OK: true, Waiting: 1, Worst: "stalled", WorstWord: "waiting"}, "wait", "waiting"},
		{"report", Signal{OK: true, Waiting: 1, Worst: "report", WorstWord: "waiting"}, "neutral", "waiting"},
		{"quiet kind", Signal{OK: true, Waiting: 1, Worst: "quiet", WorstWord: "waiting"}, "neutral", "waiting"},
		{"waiting with no worst", Signal{OK: true, Waiting: 1}, "neutral", "waiting"},
		{"quiet", Signal{OK: true}, "", "quiet"},
		{"fail kind left with nothing waiting", Signal{OK: true, Worst: "blocked", WorstWord: "blocked"}, "", "quiet"},
		{"working is still quiet", Signal{OK: true, Working: true}, "", "quiet"},
		{"unavailable", Signal{Waiting: 3, Worst: "blocked", WorstWord: "blocked"}, "unavailable", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tone, status := tc.sig.Mark(); tone != tc.tone || status != tc.status {
				t.Fatalf("mark = %q, %q; want %q, %q", tone, status, tc.tone, tc.status)
			}
		})
	}
}

func TestBadgeDropsAnAcknowledgedItemBeforeTheNextRead(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}, ackDone: map[string]bool{}}
	var pushed []int
	a.OnBadge = func(n int) { pushed = append(pushed, n) }
	a.setStatus(Status{Needs: Needs{
		Waiting: 2, Worst: "report", WorstWord: "waiting",
		Items: []Need{{Kind: "report", Report: "r1"}, {Kind: "report", Report: "r2"}},
	}})
	a.ackDone["r1"] = true
	a.syncAttention()
	if want := []int{2, 1}; !slices.Equal(pushed, want) {
		t.Fatalf("badge pushes %v, want %v", pushed, want)
	}
}

func TestBadgeSetAfterTheStateIsKnownStillReachesTheSink(t *testing.T) {
	a := &App{Model: struct{ ReadModel }{}, Fleet: Fleet{ID: "f1", Name: "alpha"}}
	a.setStatus(Status{Needs: Needs{Waiting: 3, Worst: "decision", WorstWord: "waiting"}})
	var pushed []int
	a.OnBadge = func(n int) { pushed = append(pushed, n) }
	a.syncAttention()
	a.syncAttention()
	if want := []int{3}; !slices.Equal(pushed, want) {
		t.Fatalf("badge pushes %v, want %v", pushed, want)
	}
}
