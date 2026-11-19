package desktop_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo/ui"
)

const reportBody = "Result:\n\n| Check | Outcome |\n|---|---|\n| lint | green |\n| tests | green |\n\n```go\nfmt.Println(\"hi\")\n```\n\n- [x] shipped\n- [ ] announced\n\n"

func reportApp(t *testing.T, body string) (*desktop.App, *ui.Tester) {
	t.Helper()
	return reportAppAt(t, "Result\n\n"+body, "")
}

func reportAppAt(t *testing.T, body, home string) (*desktop.App, *ui.Tester) {
	t.Helper()
	f := goldenFixture(t)
	f.fleets[0].Home = home
	rd := f.report[fleetID]
	rd.Report.Body = body
	f.report[fleetID] = rd
	td := f.task[fleetID]
	td.Reports = append([]desktop.Report(nil), td.Reports...)
	td.Reports[0].Body = body
	f.task[fleetID] = td
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	click(t, tt, "Open r1")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("screen = %v %q, want the task at r1's card", app.Screen, app.Ref)
	}
	return app, tt
}

func TestReportTableRendersAsATable(t *testing.T) {
	_, tt := reportApp(t, reportBody)
	mustText(t, tt, "Check", "Outcome", "lint", "tests", "shipped")
	for _, raw := range tt.Texts() {
		if strings.Contains(raw, "|") || strings.Contains(raw, "```") {
			t.Fatalf("raw Markdown on screen: %q", raw)
		}
	}
}

func TestReportCodeBlockCopyFillsTheClipboard(t *testing.T) {
	_, tt := reportApp(t, reportBody)
	click(t, tt, "Copy")
	if got := strings.TrimSpace(tt.Clipboard()); got != `fmt.Println("hi")` {
		t.Fatalf("clipboard = %q", tt.Clipboard())
	}
}

func TestReportLinkOpensOnlyWhatIsSafe(t *testing.T) {
	cases := []struct {
		name, dest, want, ran, doc string
	}{
		{"https", "https://github.com/atqamz/hand/pull/1", "https://github.com/atqamz/hand/pull/1", "", ""},
		{"http", "http://example.com/a", "http://example.com/a", "", ""},
		{"fleet path", "/" + fleetID + "/doc/reports/t1/a.md", "", "", "reports/t1/a.md"},
		{"other fleet path", "/" + secondID + "/doc/a.md", "", "", ""},
		{"board root", "/", "", "", ""},
		{"traversal", "/" + fleetID + "/doc/../../etc/passwd", "", "", ""},
		{"protocol relative", "//evil.example/" + fleetID + "/", "", "", ""},
		{"javascript", "javascript:alert(1)", "", "", ""},
		{"file", "file:///etc/passwd", "", "", ""},
		{"mailto", "mailto:a@example.com", "", "", ""},
		{"relative", "reports/t1/a.md", "", "", ""},
		{"backslash traversal", "/" + fleetID + "/doc/..\\x", "", "", ""},
		{"unc", "\\\\host\\x", "", "", ""},
		{"encoded nul", "/" + fleetID + "/a%00", "", "", ""},
		{"upper http", "HTTP://x", "HTTP://x", "", ""},
		{"mixed javascript", "JaVaScRiPt:x", "", "", ""},
		{"one slash http", "http:/x", "", "", ""},
		{"data", "data:text/html,x", "", "", ""},
		{"encoded dots", "/" + fleetID + "/doc/%2e%2e/x", "", "", ""},
		{"encoded slash after dots", "/" + fleetID + "/doc/..%2fx", "", "", ""},
		{"encoded slash after id", "/" + fleetID + "%2Fx", "", "", ""},
		{"long", "https://example.com/" + strings.Repeat("a", 20000), "https://example.com/" + strings.Repeat("a", 20000), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hand := installFakeHand(t)
			app, tt := reportAppAt(t, "Result\n\nOpen [the link]("+tc.dest+") now", hand.home)
			click(t, tt, "the link")
			got := tt.OpenedURLs()
			if tc.doc != "" {
				if len(got) != 0 || len(hand.calls(t)) != 0 || app.Screen != desktop.DocScreen || app.Ref != tc.doc {
					t.Fatalf("opened %q, hand ran %q, screen %v %q; want the viewer at %q", got, hand.calls(t), app.Screen, app.Ref, tc.doc)
				}
				return
			}
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("opened %q", got)
				}
				calls := hand.calls(t)
				if tc.ran == "" && len(calls) != 0 {
					t.Fatalf("hand ran %q", calls)
				}
				if tc.ran != "" && (len(calls) != 2 || !strings.HasSuffix(calls[1], " "+tc.ran)) {
					t.Fatalf("hand ran %q, want %q", calls, tc.ran)
				}
				return
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("opened %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReportBoardLinkGoesThroughHandOpen(t *testing.T) {
	for _, tc := range []struct {
		dest string
		argv []string
	}{
		{"/" + fleetID + "/task/t1", []string{"open", "t1"}},
		{"/" + fleetID + "/decision/d1#x", []string{"open", "d1"}},
		{"/" + fleetID + "/", []string{"open"}},
		{"/" + fleetID + "/doc/", nil},
		{"/" + fleetID + "/doc", nil},
	} {
		t.Run(tc.dest, func(t *testing.T) {
			hand := installFakeHand(t)
			app, tt := reportAppAt(t, "Result\n\nOpen [the link]("+tc.dest+") now", hand.home)
			click(t, tt, "the link")
			if got := tt.OpenedURLs(); len(got) != 0 {
				t.Fatalf("opened %q in the browser directly", got)
			}
			if tc.argv == nil {
				if app.OpenErr == "" || len(hand.calls(t)) != 0 {
					t.Fatalf("err %q calls %q", app.OpenErr, hand.calls(t))
				}
				return
			}
			hand.want(t, tc.argv...)
		})
	}
}

func TestPlainTextSelectsAndCopies(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	line := "s1 working"
	r, ok := tt.Find(line)
	if !ok {
		t.Fatalf("need row %q not on screen", line)
	}
	tt.Press(r.X+1, r.Y+r.H/2)
	tt.Move(r.X+r.W-1, r.Y+r.H/2)
	tt.Release(r.X+r.W-1, r.Y+r.H/2)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got == "" || !strings.Contains(line, strings.TrimSpace(got)) {
		t.Fatalf("clipboard = %q after dragging over %q", got, line)
	}
}

func TestEveryMarkdownBearingTextRendersTables(t *testing.T) {
	table := "\n\n| Colname | Cellval |\n|---|---|\n| one | two |\n"
	f := goldenFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{Role: "supervisor", At: "2026-10-31T04:40:00Z", Text: "Timeline" + table}}, tl.Entries...)
	f.timeline[fleetID] = tl
	td := f.task[fleetID]
	td.Plan = &desktop.Plan{Ref: "p1", Body: "Plan" + table}
	f.task[fleetID] = td
	ad := f.attempt[fleetID]
	ad.Transcript = desktop.Transcript{Shown: true, Turns: []desktop.Turn{{Role: "worker", At: "2026-10-31T04:41:00Z", Text: "Turn" + table}}}
	f.attempt[fleetID] = ad
	dd := f.decision[fleetID]
	dd.Decision.Question = dd.Decision.Headline + "\n\nQuestion" + table
	f.decision[fleetID] = dd

	for _, tc := range []struct{ name, ref string }{
		{"timeline", ""}, {"task", "t1"}, {"attempt", "a1"}, {"decision", "d1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newApp(t, f)
			tt := newTester(app)
			if tc.ref == "" {
			} else {
				app.OpenRef(tc.ref)
				tt.Frame()
			}
			mustText(t, tt, "Colname", "Cellval")
			for _, s := range tt.Texts() {
				if strings.Contains(s, "|---") {
					t.Fatalf("raw table on screen: %q", s)
				}
			}
		})
	}
}

func TestReportRefsOpenTheirPages(t *testing.T) {
	for _, tc := range []struct {
		ref    string
		screen desktop.Screen
		page   string
	}{
		{"t77", desktop.TaskScreen, "t77"},
		{"a88", desktop.AttemptScreen, "a88"},
		{"d99", desktop.DecisionScreen, "d99"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			hand := installFakeHand(t)
			app, tt := reportAppAt(t, "Result\n\nSee "+tc.ref+" now, `"+tc.ref+"` stays", hand.home)
			click(t, tt, tc.ref)
			if app.Screen != tc.screen || app.Ref != tc.page {
				t.Fatalf("screen %v %q, want %v %q", app.Screen, app.Ref, tc.screen, tc.page)
			}
			if got := tt.OpenedURLs(); len(got) != 0 || len(hand.calls(t)) != 0 {
				t.Fatalf("opened %q, hand ran %q", got, hand.calls(t))
			}
			if app.Err == "" || !tt.HasText(app.Err) {
				t.Fatalf("a ref to a page that does not exist shows no error: %q", app.Err)
			}
		})
	}
}

func TestReportRefToAMissingReportKeepsThePageAndSaysSo(t *testing.T) {
	hand := installFakeHand(t)
	app, tt := reportAppAt(t, "Result\n\nSee r55 now", hand.home)
	click(t, tt, "r55")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("screen %v %q, want the page the click came from", app.Screen, app.Ref)
	}
	mustText(t, tt, "The report could not be read", "not found: report r55")
}

func TestRefsInEveryBlockAreLinks(t *testing.T) {
	body := "# Plan for t11\n\n- item a22\n\n> quoted d33\n\n| ref |\n|---|\n| t44 |\n\n*emphasis a55* and **strong d66**"
	for _, ref := range []string{"t11", "a22", "d33", "t44", "a55", "d66"} {
		t.Run(ref, func(t *testing.T) {
			hand := installFakeHand(t)
			app, tt := reportAppAt(t, "Result\n\n"+body, hand.home)
			click(t, tt, ref)
			if app.Ref != ref {
				t.Fatalf("a click on %s opened %q", ref, app.Ref)
			}
		})
	}
}

func TestRefsInCodeLinksAndOtherFleetsDoNothing(t *testing.T) {
	hand := installFakeHand(t)
	body := "Result\n\n`t11` and [a22 text](https://example.com/d33) and [d44](/" + secondID + "/ref/d44) and [t55](/" + fleetID + "/ref/t99)"
	app, tt := reportAppAt(t, body, hand.home)
	if !tt.HasText("t11") {
		t.Fatal("the code span is missing")
	}
	click(t, tt, "a22 text")
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != "https://example.com/d33" {
		t.Fatalf("opened %q", got)
	}
	click(t, tt, "d44")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" || app.OpenErr != "" {
		t.Fatalf("a ref of another fleet opened %v %q, err %q", app.Screen, app.Ref, app.OpenErr)
	}
	click(t, tt, "t55")
	if app.Ref != "t99" {
		t.Fatalf("an explicit ref link opened %q", app.Ref)
	}
	if len(hand.calls(t)) != 0 {
		t.Fatalf("hand ran %q", hand.calls(t))
	}
}

func TestRefsInADocumentOpenTheirPages(t *testing.T) {
	hand := installFakeHand(t)
	f := docFixture(t)
	f.fleets[0].Home = hand.home
	f.docBytes["reports/t1/design.md"] = []byte("Go see t77 and [notes](notes.txt) now\n")
	app := newApp(t, f)
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	click(t, tt, "t77")
	if app.Screen != desktop.TaskScreen || app.Ref != "t77" {
		t.Fatalf("screen %v %q", app.Screen, app.Ref)
	}
}

func TestReportRefTextIsUnchanged(t *testing.T) {
	_, tt := reportApp(t, "see t77, a88, d99 and r55 here")
	mustText(t, tt, "t77", "a88", "d99", "r55")
	for _, raw := range tt.Texts() {
		if strings.Contains(raw, "](") || strings.Contains(raw, "/ref/") {
			t.Fatalf("link syntax on screen: %q", raw)
		}
	}
}
