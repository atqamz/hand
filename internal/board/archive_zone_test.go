package board_test

import (
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	return loc
}

type dayGroup struct {
	day, name string
	count     int
}

func TestArchiveDayGroupsFollowTheBoardZone(t *testing.T) {
	at := func(layout string) time.Time {
		v, err := time.Parse(time.RFC3339, layout)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name  string
		zone  string
		times []string
		want  []dayGroup
	}{
		{"fixed offset splits at local midnight", "Asia/Jakarta",
			[]string{"2026-10-05T16:30:00Z", "2026-10-05T17:30:00Z"},
			[]dayGroup{{"2026-10-06", "Tue 6 Oct WIB", 1}, {"2026-10-05", "Mon 5 Oct WIB", 1}}},
		{"a row exactly at local midnight opens the new day", "Asia/Jakarta",
			[]string{"2026-10-05T16:59:59Z", "2026-10-05T17:00:00Z"},
			[]dayGroup{{"2026-10-06", "Tue 6 Oct WIB", 1}, {"2026-10-05", "Mon 5 Oct WIB", 1}}},
		{"a 45-minute offset puts local midnight off the hour", "Asia/Kathmandu",
			[]string{"2026-10-05T18:14:59Z", "2026-10-05T18:15:00Z"},
			[]dayGroup{{"2026-10-06", "Tue 6 Oct +0545", 1}, {"2026-10-05", "Mon 5 Oct +0545", 1}}},
		{"UTC host keeps one UTC day", "UTC",
			[]string{"2026-10-05T16:30:00Z", "2026-10-05T17:30:00Z"},
			[]dayGroup{{"2026-10-05", "Mon 5 Oct UTC", 2}}},
		{"the day a DST change repeats an hour stays one group", "America/New_York",
			[]string{"2026-11-01T03:30:00Z", "2026-11-01T04:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-02T04:30:00Z", "2026-11-02T05:30:00Z"},
			[]dayGroup{{"2026-11-02", "Mon 2 Nov EST", 1}, {"2026-11-01", "Sun 1 Nov EST", 4}, {"2026-10-31", "Sat 31 Oct EDT", 1}}},
		{"the short day of a DST change is one group", "America/New_York",
			[]string{"2027-03-14T04:30:00Z", "2027-03-14T06:30:00Z", "2027-03-15T03:30:00Z", "2027-03-15T04:30:00Z"},
			[]dayGroup{{"2027-03-15", "Mon 15 Mar EDT", 1}, {"2027-03-14", "Sun 14 Mar EDT", 2}, {"2027-03-13", "Sat 13 Mar EST", 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loc := zone(t, c.zone)
			a := newArchive(t)
			for i, v := range c.times {
				a.finish(t, "hand", "Task "+string(rune('A'+i)), state.StatusDone, at(v))
			}
			h := a.handlerIn(loc)
			_, got := apiInto[board.APITaskSearch](t, h, "/api/v1/tasks/search")
			if len(got.Groups) != len(c.want) {
				t.Fatalf("groups = %+v, want %+v", got.Groups, c.want)
			}
			body := get(t, h, "/tasks")
			for i, g := range got.Groups {
				have := dayGroup{g.Day, g.Name, g.Count}
				if have != c.want[i] {
					t.Fatalf("group %d = %+v, want %+v", i, have, c.want[i])
				}
				contains(t, "archive page", body, `<h3 class="group">`+strings.ReplaceAll(g.Name, "+", "&#43;")+` <span class="badge">`)
			}
			for _, v := range c.times {
				contains(t, "row clock", body, `datetime="`+v+`" data-clock>`+at(v).In(loc).Format("15:04")+`</time>`)
			}
		})
	}
}

func TestArchiveDayGroupsDefaultToTheHostZone(t *testing.T) {
	old := time.Local
	time.Local = zone(t, "Asia/Jakarta")
	t.Cleanup(func() { time.Local = old })
	a := newArchive(t)
	a.finish(t, "hand", "Late", state.StatusDone, time.Date(2026, 10, 5, 17, 30, 0, 0, time.UTC))
	_, got := apiInto[board.APITaskSearch](t, board.New(a.st, token, board.Options{Controls: true}), "/api/v1/tasks/search")
	if len(got.Groups) != 1 || got.Groups[0].Day != "2026-10-06" || got.Groups[0].Name != "Tue 6 Oct WIB" {
		t.Fatalf("groups = %+v", got.Groups)
	}
}
