package board_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func reply(text string) string {
	return `{"type":"assistant","timestamp":"2026-09-28T01:00:01Z","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestChatKeepsOrderWithoutNumbers(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("first reply"), reply("second reply"), reply("third reply")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	third, second, first := strings.Index(tl, "third reply"), strings.Index(tl, "second reply"), strings.Index(tl, "first reply")
	if first < 0 || second < 0 || third < 0 || !(third < second && second < first) {
		t.Fatalf("replies out of order (%d %d %d):\n%s", third, second, first, tl)
	}
	contains(t, "slug", tl, `<header class="slug"><span class="who">s1</span>`)
	lacks(t, "numbers", tl, "NO. ")
	older := get(t, fx.handler(), "/supervisor/log?before=4")
	contains(t, "paging", older, "second reply", "first reply")
	lacks(t, "paging", older, "third reply")
}

func TestYourNotesAreYours(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("please look")})
	if _, err := fx.st.AddSupervisorInput(context.Background(), "later please"); err != nil {
		t.Fatal(err)
	}
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "notes", tl, `<article class="card from-you" data-role="operator"`, `<span class="who">you</span>`, "please look", `<span class="delivery">queued</span>`, "later please")
}

func TestHandLinesAreOneLine(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord(`[hand v1 wake]\ndecision.answered d1`)})
	contains(t, "hand line", region(get(t, fx.handler(), "/"), "timeline"), `<p class="hand-line" data-no="1"><span class="ring" aria-hidden="true"></span>`, `wake: decision answered <a class="ref" href="/ref/d1">d1</a>`)
}

func TestTheWorkingLineNamesTheSupervisor(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "working", tl, "data-working", "s1 working")
	lacks(t, "working", tl, "printhead")
}

func TestRefsLinkInDispatches(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("started t1 for you")})
	contains(t, "refs", region(get(t, fx.handler(), "/"), "timeline"), `<a class="ref" href="/ref/t1">t1</a>`)
}

func at(record, ts string) string {
	return strings.Replace(record, `"timestamp":"2026-09-28T01:00:0`, `"timestamp":"`+ts+`","_":"`, 1)
}

func TestChatCardsHaveAHeaderStrip(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("please look"), reply("on it")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "cards", tl, `<article class="card from-sup" data-role="supervisor"`, `<header class="slug"><span class="who">s1</span>`, `<div class="copy md">`, `<article class="card from-you" data-role="operator"`, `<span class="who">you</span>`)
}

func TestAForcedMessageSaysTypedAnyway(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	typed, err := fx.st.AddSupervisorInput(ctx, "hello there")
	if err != nil {
		t.Fatal(err)
	}
	sent, err := fx.st.AddSupervisorInput(ctx, "plain one")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, typed.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, sent.ID, false); err != nil {
		t.Fatal(err)
	}
	claudeLog(t, fx, []string{userRecord("hello there"), userRecord("plain one")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "delivery", tl, `id="msg-i1"`, `id="msg-i2"`, `<span class="delivery">typed anyway <time`, `<span class="delivery">delivered <time`)
}

func TestWakesGroup(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	wake := userRecord(`[hand v1 wake]\ndecision.answered d1`)
	claudeLog(t, fx, []string{at(wake, "2026-09-28T12:28:00Z"), at(wake, "2026-09-28T12:28:30Z"), at(wake, "2026-09-28T12:29:00Z"), at(reply("done"), "2026-09-28T12:30:00Z")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "wakes", tl, `<details class="hand-line wakes"`, `3 wakes · <time datetime="2026-09-28T12:28:00Z" data-clock>`, `–<time datetime="2026-09-28T12:29:00Z" data-clock>`)
	group := tl[strings.Index(tl, `<details class="hand-line wakes"`):]
	group = group[:strings.Index(group, "</details>")]
	if n := strings.Count(group, `<p class="hand-line"`); n != 3 {
		t.Fatalf("the group holds %d wake lines:\n%s", n, group)
	}
}

func TestLaterRefsLeaveTheTabOrder(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("see t1, t2 and t3")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "refs", tl, `<a class="ref" href="/ref/t1"`, `<a class="ref" tabindex="-1" href="/ref/t2"`, `<a class="ref" tabindex="-1" href="/ref/t3"`)
}

func TestTheWorkingLineLinksYourMessage(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	in, err := fx.st.AddSupervisorInput(ctx, "fix it")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
		t.Fatal(err)
	}
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "working", tl, `s1 working · `, `on your <a href="#msg-i1">`)
}

func TestDayDividers(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{at(userRecord("go"), "2026-09-27T10:00:00Z"), at(reply("yesterday"), "2026-09-27T10:00:05Z"), at(reply("today"), "2026-09-28T10:00:00Z")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	day := `<p class="day"><span>Mon 28 Sep UTC</span></p>`
	today, divider, yesterday := strings.Index(tl, "today"), strings.Index(tl, day), strings.Index(tl, "yesterday")
	if strings.Count(tl, day) != 1 || !(today < divider && divider < yesterday) {
		t.Fatalf("no single divider between the days (%d %d %d):\n%s", today, divider, yesterday, tl)
	}
	contains(t, "clock under the divider", tl, `>10:00</time>`)
	contains(t, "date on the oldest day", tl, `>Sep 26 00:00 UTC</time>`)
}

func TestRepeatedMessagesKeepTheirOwnDelivery(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	for range 2 {
		in, err := fx.st.AddSupervisorInput(ctx, "ok")
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.st.DeliverSupervisorInput(ctx, in.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	claudeLog(t, fx, []string{userRecord("ok"), userRecord("ok")})
	contains(t, "delivery", region(get(t, fx.handler(), "/"), "timeline"), `id="msg-i1"`, `id="msg-i2"`)
}
