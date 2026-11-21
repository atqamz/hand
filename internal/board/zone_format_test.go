package board_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestTheTimelineTimesFollowTheBoardZone(t *testing.T) {
	cases := []struct {
		zone     string
		oldest   string
		newest   string
		dividers []string
		absent   []string
	}{
		{"UTC", "Sep 26 00:00 UTC", "20:00", []string{"Mon 28 Sep UTC"}, []string{"Tue 29 Sep", "Sun 27 Sep"}},
		{"Asia/Jakarta", "Sep 26 07:00 WIB", "03:00", []string{"Tue 29 Sep WIB", "Mon 28 Sep WIB"}, []string{"UTC"}},
		{"America/New_York", "Sep 25 20:00 EDT", "16:00", []string{"Mon 28 Sep EDT", "Sun 27 Sep EDT"}, []string{"UTC", "Tue 29 Sep"}},
		{"Asia/Kathmandu", "Sep 26 05:45 &#43;0545", "01:45", []string{"Tue 29 Sep &#43;0545", "Mon 28 Sep &#43;0545"}, []string{"UTC"}},
	}
	for _, c := range cases {
		t.Run(c.zone, func(t *testing.T) {
			fx := newFixture(t)
			fx.options.Zone = zone(t, c.zone)
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			claudeLog(t, fx, []string{at(userRecord("go"), "2026-09-28T01:00:00Z"), at(reply("yesterday"), "2026-09-28T01:00:05Z"), at(reply("today"), "2026-09-28T20:00:00Z")})
			tl := region(get(t, fx.handler(), "/"), "timeline")
			contains(t, "oldest stamp", tl, `>`+c.oldest+`</time>`)
			contains(t, "clock under a divider", tl, `>`+c.newest+`</time>`)
			for _, d := range c.dividers {
				contains(t, "divider", tl, `<span class="badge">`+d+`</span>`)
			}
			lacks(t, "timeline", tl, c.absent...)
		})
	}
}

func TestWakesGroupByTheBoardZoneDay(t *testing.T) {
	for _, c := range []struct {
		zone   string
		stamps [4]string
		groups int
	}{
		{"UTC", [4]string{"16:58", "16:59", "17:01", "17:02"}, 1},
		{"Asia/Jakarta", [4]string{"16:58", "16:59", "17:01", "17:02"}, 2},
		{"Asia/Jakarta", [4]string{"18:13", "18:14", "18:16", "18:17"}, 1},
		{"Asia/Kathmandu", [4]string{"18:13", "18:14", "18:16", "18:17"}, 2},
		{"America/New_York", [4]string{"03:58", "03:59", "04:01", "04:02"}, 2},
		{"UTC", [4]string{"03:58", "03:59", "04:01", "04:02"}, 1},
	} {
		t.Run(c.zone+" "+c.stamps[0], func(t *testing.T) {
			fx := newFixture(t)
			fx.options.Zone = zone(t, c.zone)
			fx.supervisor(t, state.AttemptRunning, "gen-1")
			wake := userRecord(`[hand v1 wake]\ndecision.answered d1`)
			var lines []string
			for _, s := range c.stamps {
				lines = append(lines, at(wake, "2026-09-28T"+s+":00Z"))
			}
			claudeLog(t, fx, lines)
			tl := region(get(t, fx.handler(), "/"), "timeline")
			if n := strings.Count(tl, `<details class="hand-line wakes"`); n != c.groups {
				t.Fatalf("%d wake groups in %s, want %d:\n%s", n, c.zone, c.groups, tl)
			}
		})
	}
}

func TestTheWorkingLineClockFollowsTheBoardZone(t *testing.T) {
	for _, c := range []struct{ zone, clock string }{{"UTC", "00:00"}, {"Asia/Jakarta", "07:00"}, {"America/New_York", "20:00"}, {"Asia/Kathmandu", "05:45"}} {
		t.Run(c.zone, func(t *testing.T) {
			fx := newFixture(t)
			fx.options.Zone = zone(t, c.zone)
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
			contains(t, "working line", tl, `data-clock>`+c.clock+`</time> message</a>`)
		})
	}
}

func TestAWaitDateFollowsTheBoardZone(t *testing.T) {
	for _, c := range []struct{ zone, date string }{{"UTC", "Sep 29"}, {"Asia/Jakarta", "Sep 29"}, {"America/New_York", "Sep 28"}, {"Asia/Kathmandu", "Sep 29"}} {
		t.Run(c.zone, func(t *testing.T) {
			st, _ := clocked(t)
			one := active(t, st, "Review outside PR")
			wait(t, st, one.ID, "outside PR")
			h := board.New(st, token, board.Options{Zone: zone(t, c.zone)})
			line := `Waits until ` + c.date + `: outside PR`
			contains(t, "task card", get(t, h, "/task/t1"), line)
			contains(t, "tasks row", region(get(t, h, "/"), "tasks"), line)
			contains(t, "tasks page", get(t, h, "/tasks?status=active"), line)
		})
	}
}
