package board_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

var checkRow = regexp.MustCompile(`<li class="check" data-state="([a-z]+)">([\s\S]*?)</li>`)

func checks(body string) map[string]string {
	out := map[string]string{}
	for _, m := range checkRow.FindAllStringSubmatch(region(body, "tasks"), -1) {
		ref := regexp.MustCompile(`href="[^"]*/task/(t[0-9]+)"`).FindStringSubmatch(m[2])
		if ref != nil {
			out[ref[1]] = m[1] + "|" + m[2]
		}
	}
	return out
}

func TestChecksAreOneLinePerTask(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	running := active(t, st, "Running work")
	a := attempt(t, st, running.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(ctx, a.ID, state.ReportProgress, "PR: https://github.com/atqamz/hand/pull/7"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AckReport(ctx, 1, "operator"); err != nil {
		t.Fatal(err)
	}
	asking := active(t, st, "Needs an answer")
	if _, err := st.Ask(ctx, asking.ID, "Which way?"); err != nil {
		t.Fatal(err)
	}
	broken := active(t, st, "Broken work")
	attempt(t, st, broken.ID, state.AttemptFailed, "launch did not finish")
	if _, err := st.Ask(ctx, broken.ID, "Retry?"); err != nil {
		t.Fatal(err)
	}
	finished := active(t, st, "Finished work")
	if _, err := st.Transition(ctx, finished.ID, state.StatusDone); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask(ctx, "hand", "Inbox idea", ""); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	rows := checks(get(t, h, "/"))
	want := map[string]string{"t1": "running", "t2": "waiting", "t3": "failing", "t5": "idle"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %v", rows)
	}
	for ref, state := range want {
		if got, _, _ := strings.Cut(rows[ref], "|"); got != state {
			t.Fatalf("%s state = %q, want %q (%v)", ref, got, state, rows)
		}
	}
	contains(t, "running row", rows["t1"], "Running work", "a1", "codex", "gpt-6-luna", `href="https://github.com/atqamz/hand/pull/7"`)
	if strings.Contains(rows["t1"], "\n") {
		t.Fatalf("a check row spans lines: %q", rows["t1"])
	}
	all := checks(get(t, h, "/?all=1"))
	if got, _, _ := strings.Cut(all["t4"], "|"); got != "passing" {
		t.Fatalf("done task under ?all=1 = %q", all["t4"])
	}
}

func TestTheStatusIsAPill(t *testing.T) {
	pill := regexp.MustCompile(`<span class="pill" data-state="([a-z]+)">`)
	state := func(t *testing.T, body string) string {
		t.Helper()
		m := pill.FindAllStringSubmatch(region(body, "status"), -1)
		if len(m) != 1 {
			t.Fatalf("status pills = %d:\n%s", len(m), region(body, "status"))
		}
		return m[0][1]
	}
	none := newFixture(t)
	if got := state(t, get(t, none.handler(), "/")); got != "neutral" {
		t.Fatalf("no supervisor = %q", got)
	}
	working := newFixture(t)
	working.supervisor(t, "running", "gen-1")
	body := get(t, working.handler(), "/")
	if got := state(t, body); got != "working" {
		t.Fatalf("working = %q", got)
	}
	contains(t, "working status", region(body, "status"), `action="/supervisor/stop"`, `action="/supervisor/interrupt"`, "0 queued")
	blocked := newFixture(t)
	blocked.status = "blocked"
	blocked.supervisor(t, "running", "gen-1")
	if got := state(t, get(t, blocked.handler(), "/")); got != "failing" {
		t.Fatalf("blocked = %q", got)
	}
	stale := newFixture(t)
	stale.supervisor(t, "running", "gen-0")
	if got := state(t, get(t, stale.handler(), "/")); got != "failing" {
		t.Fatalf("stale = %q", got)
	}
	stopped := newFixture(t)
	stopped.supervisor(t, "stopped", "gen-1")
	if got := state(t, get(t, stopped.handler(), "/")); got != "neutral" {
		t.Fatalf("stopped = %q", got)
	}
}

func TestTimelineCommentsAndEvents(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, "running", "gen-1")
	claudeLog(t, fx, []string{
		userRecord(`[hand v1 wake]\nr3 from a2 done`),
		userRecord("please **look**"),
		`{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"Done: **shipped** it"}]}}`,
	})
	if _, err := fx.st.AddSupervisorInput(context.Background(), "later please"); err != nil {
		t.Fatal(err)
	}
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", tl,
		`<article class="comment" data-role="supervisor">`, "<strong>shipped</strong>",
		`<article class="comment" data-role="operator">`, "please **look**",
		`<div class="event">`, "wake: r3 from a2 done",
		"Queued", "later please")
	lacks(t, "timeline", tl, "<strong>look</strong>", "queued: later please")
}
