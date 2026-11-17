package board_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func sendInput(t *testing.T, fx *fixture, body string) int64 {
	t.Helper()
	in, err := fx.st.AddSupervisorInput(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	return in.ID
}

func TestTheLineUnderAQueuedMessageNamesWhyItWaits(t *testing.T) {
	for _, c := range []struct{ reason, detail, want string }{
		{state.ReasonBlocked, "Esc to cancel", `queued · blocked: Esc to cancel</p>`},
		{state.ReasonLimited, "You've hit your session limit · resets 6:10am", `queued · limited: You&#39;ve hit your session limit · resets 6:10am</p>`},
		{state.ReasonLost, "opencode did not take the resume note", `queued · lost: opencode did not take the resume note</p>`},
		{state.ReasonSwitching, "opus high", `queued · switching to opus high</p>`},
		{state.ReasonNoSupervisor, "", `queued · no supervisor</p>`},
		{state.ReasonHeld, "waiting for omp to start its session", `queued · waiting for omp to start its session</p>`},
		{state.ReasonTurn, "2026-09-28T04:41:00Z", `queued · working since last delivery <time datetime="2026-09-28T04:41:00Z" data-clock>04:41</time></p>`},
	} {
		t.Run(c.reason, func(t *testing.T) {
			fx := newFixture(t)
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			sendInput(t, fx, "later please")
			if err := fx.st.HoldSupervisorInputs(context.Background(), c.reason, c.detail); err != nil {
				t.Fatal(err)
			}
			contains(t, c.reason, region(get(t, fx.handler(), "/"), "timeline"), `id="msg-i1"`, `data-state="queued">`, c.want)
		})
	}
}

func TestAQueuedMessageWithNoReasonYetSaysOnlyQueued(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	sendInput(t, fx, "later please")
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "queued", tl, `data-state="queued">queued</p>`)
	lacks(t, "queued", tl, "queued ·")
}

func TestTheLineFollowsAMessageFromDeliveredToAnswered(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	id := sendInput(t, fx, "status?")
	if err := fx.st.DeliverSupervisorInput(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{userRecord("status?")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "delivered", tl, `id="msg-i1"`, `data-state="delivered">delivered <time datetime="2026-09-26T00:00:00Z" data-clock>00:00</time></p>`)
	lacks(t, "delivered", tl, `data-state="answered"`)
	if err := fx.st.AnswerSupervisorInput(ctx, id, "2026-09-28T01:00:09Z"); err != nil {
		t.Fatal(err)
	}
	tl = region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "answered", tl, `id="msg-i1"`, `data-state="answered">answered <time datetime="2026-09-28T01:00:09Z" data-clock>01:00</time></p>`)
	lacks(t, "answered", tl, `data-state="delivered"`)
}

func TestADeliveredMessageTheHarnessHasNotRecordedStaysInTheConversationWhileTheSupervisorWorks(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	id := sendInput(t, fx, "behind the turn")
	if err := fx.st.DeliverSupervisorInput(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{userRecord("an older message")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "working", tl, `id="msg-i1"`, "behind the turn", `data-state="delivered">delivered <time`)
	if n := strings.Count(tl, "behind the turn"); n != 1 {
		t.Fatalf("the message shows %d times:\n%s", n, tl)
	}
	fx.status = "idle"
	tl = region(get(t, fx.handler(), "/"), "timeline")
	lacks(t, "idle and long past", tl, "behind the turn")
}

func TestADeliveredMessageOlderThanOneTheHarnessRecordedIsNotShownAgain(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	lost, newer := sendInput(t, fx, "never recorded"), sendInput(t, fx, "recorded")
	for _, id := range []int64{lost, newer} {
		if err := fx.st.DeliverSupervisorInput(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	claudeLog(t, fx, []string{userRecord("recorded")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "recorded", tl, `id="msg-i2"`)
	lacks(t, "older unpaired", tl, "never recorded", `id="msg-i1"`)
}

func TestAMessageQueuedBehindARecordedOneStaysInTheConversationWhileTheSupervisorWorks(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	one, two := sendInput(t, fx, "one"), sendInput(t, fx, "two")
	for _, id := range []int64{one, two} {
		if err := fx.st.DeliverSupervisorInput(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	claudeLog(t, fx, []string{userRecord("one")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "both", tl, `id="msg-i1"`, `id="msg-i2"`, "two")
	if n := strings.Count(tl, `id="msg-i2"`); n != 1 {
		t.Fatalf("the queued message shows %d times:\n%s", n, tl)
	}
}

func TestTwoMessagesWithTheSameTextKeepTheirOwnStateWhileTheLaterOneIsUnrecorded(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	older, newer := sendInput(t, fx, "continue"), sendInput(t, fx, "continue")
	for _, id := range []int64{older, newer} {
		if err := fx.st.DeliverSupervisorInput(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.st.AnswerSupervisorInput(ctx, older, "2026-09-28T01:00:09Z"); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{at(userRecord("continue"), "2026-09-26T00:00:00Z")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "both", tl, `id="msg-i1"`, `id="msg-i2"`, `data-state="answered">answered`, `data-state="delivered">delivered`)
	if i1, i2, a, d := strings.Index(tl, `id="msg-i1"`), strings.Index(tl, `id="msg-i2"`), strings.Index(tl, `data-state="answered"`), strings.Index(tl, `data-state="delivered"`); !(i2 < d && d < i1 && i1 < a) {
		t.Fatalf("states are on the wrong messages (i1 %d i2 %d answered %d delivered %d):\n%s", i1, i2, a, d, tl)
	}
}

func TestAShortReplyBeforeAToolCallShowsInTheConversation(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{
		userRecord("status?"),
		`{"type":"assistant","timestamp":"2026-09-28T01:00:02Z","message":{"stop_reason":"tool_use","content":[{"type":"text","text":"Looking now."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"hand orient"}}]}}`,
		`{"type":"assistant","timestamp":"2026-09-28T01:00:03Z","message":{"stop_reason":"tool_use","content":[{"type":"text","text":"Reading the plan."}]}}`,
	})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "first reply", tl, "Looking now.")
	lacks(t, "chatter", tl, "Reading the plan.")
}
