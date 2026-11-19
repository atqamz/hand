package desktop_test

import (
	"math"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo/ui"
)

func transcriptApp(t *testing.T, entries ...desktop.Entry) (*desktop.App, *ui.Tester) {
	t.Helper()
	inUTC(t)
	f := goldenFixture(t)
	f.fleets[0].Home = installFakeHand(t).home
	tl := f.timeline[fleetID]
	tl.Entries = entries
	f.timeline[fleetID] = tl
	app := newApp(t, f)
	return app, sized(t, app, 1100, 760, false)
}

func operator(text string) desktop.Entry {
	return desktop.Entry{Role: "operator", No: 9, At: "2026-10-31T12:06:00Z", Delivery: "delivered", Delivered: "2026-10-31T12:06:00Z", Text: text}
}

func surfaces(text string) map[string][]desktop.Entry {
	return map[string][]desktop.Entry{
		"operator message": {operator(text)},
		"hand line":        {wake("2026-10-31T12:05:00Z", text)},
		"opened wakes": {{Role: "wakes", No: 9, At: "2026-10-30T13:34:00Z", Wakes: []desktop.Entry{
			wake("2026-10-30T13:30:00Z", "first"), wake("2026-10-30T13:34:00Z", text),
		}}},
	}
}

func TestScenesRefsInTheOperatorMessageAndHandLinesAreLinks(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			inUTC(t)
			f := goldenFixture(t)
			tl := f.timeline[fleetID]
			tl.Entries = []desktop.Entry{
				{Role: "operator", No: 9, At: "2026-10-31T12:06:00Z", Delivery: "delivered", Delivered: "2026-10-31T12:06:00Z",
					Text: "Look at t1 and a1, then answer d1 after r1.\n\n  keep *this* and `t2` as typed, w5 and #6 too\n- not a list: 2*3*4 <b>"},
				wake("2026-10-31T12:05:00Z", "attempt reported a1"),
				wake("2026-10-31T12:04:00Z", "decision d1 opened for t1 (r1), `t2` stays plain, w5 too"),
				wake("2026-10-31T12:03:00Z", "a1 and t1 and d1 and r1 and a long tail that goes on and on past the end of the line so that it must be cut off by the one line limit of a hand line"),
			}
			f.timeline[fleetID] = tl
			app := newApp(t, f)
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, "attempt reported a1")
			shotSized(t, tt, "78-operator-refs-"+name, 1100, 760)
		})
	}
}

func openWakes(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if name == "opened wakes" {
		click(t, tt, "2 wakes")
	}
}

func TestRefsInTheOperatorMessageAndHandLinesOpenTheirPages(t *testing.T) {
	for surface := range surfaces("") {
		for _, tc := range []struct {
			ref    string
			screen desktop.Screen
		}{
			{"t77", desktop.TaskScreen},
			{"a88", desktop.AttemptScreen},
			{"d99", desktop.DecisionScreen},
		} {
			t.Run(surface+"/"+tc.ref, func(t *testing.T) {
				app, tt := transcriptApp(t, surfaces("see t77, a88 and d99 now")[surface]...)
				openWakes(t, tt, surface)
				click(t, tt, tc.ref)
				if app.Screen != tc.screen || app.Ref != tc.ref {
					t.Fatalf("screen %v %q, want %v %q", app.Screen, app.Ref, tc.screen, tc.ref)
				}
				if got := tt.OpenedURLs(); len(got) != 0 {
					t.Fatalf("opened %q", got)
				}
				if app.Err == "" || !tt.HasText(app.Err) {
					t.Fatalf("a ref to a page that does not exist shows no error: %q", app.Err)
				}
			})
		}
	}
}

func TestAReportRefInTheOperatorMessageAndHandLinesOpensItsTask(t *testing.T) {
	for surface, entries := range surfaces("see r1 now") {
		t.Run(surface, func(t *testing.T) {
			app, tt := transcriptApp(t, entries...)
			openWakes(t, tt, surface)
			click(t, tt, "r1")
			if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
				t.Fatalf("screen %v %q, want the task at r1's card", app.Screen, app.Ref)
			}
		})
	}
}

func TestAMissingReportRefInTheOperatorMessageAndHandLinesSaysSo(t *testing.T) {
	for surface, entries := range surfaces("see r55 now") {
		t.Run(surface, func(t *testing.T) {
			app, tt := transcriptApp(t, entries...)
			openWakes(t, tt, surface)
			click(t, tt, "r55")
			if app.Screen != desktop.BoardScreen {
				t.Fatalf("screen %v, want the page the click came from", app.Screen)
			}
			mustText(t, tt, "not found: report r55")
		})
	}
}

func TestPlainRefsStayPlainInTheOperatorMessageAndHandLines(t *testing.T) {
	text := "t33 and `t11` [a22](https://example.com/a22) w5 #6 owner/repo#7 xt8 t09 d10.go"
	for surface, entries := range surfaces(text) {
		t.Run(surface, func(t *testing.T) {
			app, tt := transcriptApp(t, entries...)
			openWakes(t, tt, surface)
			for _, plain := range []string{"t11", "a22", "w5", "#6", "t8", "t09", "d10"} {
				if r, ok := tt.Find(plain); ok {
					t.Errorf("%q is an element of its own at %v", plain, r)
				}
			}
			click(t, tt, "t33")
			if app.Screen != desktop.TaskScreen || app.Ref != "t33" {
				t.Fatalf("screen %v %q", app.Screen, app.Ref)
			}
			if got := tt.OpenedURLs(); len(got) != 0 {
				t.Fatalf("opened %q", got)
			}
		})
	}
}

func TestATextWithNoRefIsUnchanged(t *testing.T) {
	text := "run the *tests* and `lint`, w5 #6\n- not a list"
	for surface, entries := range surfaces(text) {
		t.Run(surface, func(t *testing.T) {
			_, tt := transcriptApp(t, entries...)
			openWakes(t, tt, surface)
			want := text
			if surface != "operator message" {
				want = strings.ReplaceAll(text, "\n", " ")
			}
			found := false
			for _, raw := range tt.Texts() {
				found = found || raw == text
			}
			if !found && !tt.HasText(want) {
				t.Fatalf("text missing in %q", tt.Texts())
			}
		})
	}
}

func TestTheOperatorMessageWithRefsKeepsWhatWasTyped(t *testing.T) {
	_, tt := transcriptApp(t, operator("Look at t71 *now* and a71, 2*3*4 <b> &amp; _x_ # h\n\n  1. one\n- two\\\n[l](u) ~~s~~ r71"))
	var all []string
	for _, raw := range tt.Texts() {
		all = append(all, raw)
	}
	got := strings.Join(all, "")
	for _, want := range []string{"*now*", "2*3*4", "<b>", "&amp;", "_x_", "# h", "1. one", "- two\\", "[l](u)", "~~s~~"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not on screen as typed: %q", want, got)
		}
	}
	for _, raw := range all {
		if strings.Contains(raw, "](") && !strings.Contains(raw, "[l](u)") || strings.Contains(raw, "/ref/") {
			t.Fatalf("link syntax on screen: %q", raw)
		}
	}
}

func TestALongHandLineStaysOneLineAndKeepsItsClock(t *testing.T) {
	long := "a72 " + strings.Repeat("and then it goes on ", 400) + "t79"
	_, tt := transcriptApp(t, wake("2026-10-31T12:05:00Z", "a71 short"), wake("2026-10-31T12:04:00Z", long))
	short, ok1 := tt.Find("a71")
	cut, ok2 := tt.Find("a72")
	if !ok1 || !ok2 {
		t.Fatalf("refs missing: %v %v", ok1, ok2)
	}
	if cut.H != short.H || math.Abs(float64(cut.Y-short.Y)) > float64(2*short.H) {
		t.Fatalf("the long line is not one line: short %v, long %v", short, cut)
	}
	if r, ok := tt.Find("Oct 31, 12:04"); !ok || r.X+r.W > 1100 || r.Y > cut.Y+cut.H || r.Y+r.H < cut.Y {
		t.Fatalf("the clock of the long line is at %v", r)
	}
	if _, ok := tt.Find("t79"); ok {
		t.Fatal("the cut-off ref is still an element")
	}
}

func TestAVeryLongOperatorLineStillLinksItsRefs(t *testing.T) {
	app, tt := transcriptApp(t, operator("t55 "+strings.Repeat("word ", 5000)+"a66"))
	click(t, tt, "a66")
	if app.Screen != desktop.AttemptScreen || app.Ref != "a66" {
		t.Fatalf("screen %v %q", app.Screen, app.Ref)
	}
}

func TestUnicodeNeighboursOfRefsInTheOperatorMessageAndHandLines(t *testing.T) {
	text := "日t61 éa62 日 t63 日 t64日 d65é (r66)、d67。"
	for surface, entries := range surfaces(text) {
		t.Run(surface, func(t *testing.T) {
			_, tt := transcriptApp(t, entries...)
			openWakes(t, tt, surface)
			for _, want := range []string{"t63", "r66"} {
				if _, ok := tt.Find(want); !ok {
					t.Errorf("%s is not a link", want)
				}
			}
			for _, plain := range []string{"t61", "a62", "t64", "d65"} {
				if _, ok := tt.Find(plain); ok {
					t.Errorf("%s is a link", plain)
				}
			}
		})
	}
}

func TestSelectingAndCopyingTheOperatorMessageAndHandLinesIsUnchanged(t *testing.T) {
	typed := "alpha t71 beta, a72 *gamma* & 2*3 end\nsecond line r73 <x>"
	_, tt := transcriptApp(t, operator(typed))
	r, ok := tt.Find(typed)
	if !ok {
		t.Fatalf("the message is not one text: %q", tt.Texts())
	}
	tt.Press(r.X+1, r.Y+2)
	tt.Move(r.X+r.W-1, r.Y+r.H-2)
	tt.Release(r.X+r.W-1, r.Y+r.H-2)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != typed {
		t.Fatalf("clipboard = %q, want %q", got, typed)
	}

	line := "attempt a71 reported, t72 ok"
	_, tt = transcriptApp(t, wake("2026-10-31T12:05:00Z", line))
	r, ok = tt.Find(line)
	if !ok {
		t.Fatalf("the line is not one text: %q", tt.Texts())
	}
	tt.Press(r.X+1, r.Y+2)
	tt.Move(r.X+r.W+200, r.Y+r.H-2)
	tt.Release(r.X+r.W+200, r.Y+r.H-2)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); !strings.HasPrefix(got, line+"\n") {
		t.Fatalf("clipboard = %q, want it to start with %q", got, line)
	}
}
