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
	return reportAppAt(t, body, "")
}

func reportAppAt(t *testing.T, body, home string) (*desktop.App, *ui.Tester) {
	t.Helper()
	f := goldenFixture(t)
	f.fleets[0].Home = home
	rd := f.report[fleetID]
	rd.Report.Body = body
	f.report[fleetID] = rd
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Board")
	click(t, tt, "Cookie fixed")
	if app.Screen != desktop.ReportScreen {
		t.Fatalf("screen = %v, want the report", app.Screen)
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
		name, dest, want, ran string
	}{
		{"https", "https://github.com/atqamz/hand/pull/1", "https://github.com/atqamz/hand/pull/1", ""},
		{"http", "http://example.com/a", "http://example.com/a", ""},
		{"fleet path", "/" + fleetID + "/doc/reports/t1/a.md", "", "open --doc reports/t1/a.md"},
		{"other fleet path", "/" + secondID + "/doc/a.md", "", ""},
		{"board root", "/", "", ""},
		{"traversal", "/" + fleetID + "/doc/../../etc/passwd", "", ""},
		{"protocol relative", "//evil.example/" + fleetID + "/", "", ""},
		{"javascript", "javascript:alert(1)", "", ""},
		{"file", "file:///etc/passwd", "", ""},
		{"mailto", "mailto:a@example.com", "", ""},
		{"relative", "reports/t1/a.md", "", ""},
		{"backslash traversal", "/" + fleetID + "/doc/..\\x", "", ""},
		{"unc", "\\\\host\\x", "", ""},
		{"encoded nul", "/" + fleetID + "/a%00", "", ""},
		{"upper http", "HTTP://x", "HTTP://x", ""},
		{"mixed javascript", "JaVaScRiPt:x", "", ""},
		{"one slash http", "http:/x", "", ""},
		{"data", "data:text/html,x", "", ""},
		{"encoded dots", "/" + fleetID + "/doc/%2e%2e/x", "", ""},
		{"encoded slash after dots", "/" + fleetID + "/doc/..%2fx", "", ""},
		{"encoded slash after id", "/" + fleetID + "%2Fx", "", ""},
		{"long", "https://example.com/" + strings.Repeat("a", 20000), "https://example.com/" + strings.Repeat("a", 20000), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hand := installFakeHand(t)
			_, tt := reportAppAt(t, "Open [the link]("+tc.dest+") now", hand.home)
			click(t, tt, "the link")
			got := tt.OpenedURLs()
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
		{"/" + fleetID + "/doc/reports/t1/a.md", []string{"open", "--doc", "reports/t1/a.md"}},
		{"/" + fleetID + "/doc/reports/t1/my%20notes%231.md?x=1#top", []string{"open", "--doc", "reports/t1/my notes#1.md"}},
		{"/" + fleetID + "/doc/", nil},
		{"/" + fleetID + "/doc", nil},
	} {
		t.Run(tc.dest, func(t *testing.T) {
			hand := installFakeHand(t)
			app, tt := reportAppAt(t, "Open [the link]("+tc.dest+") now", hand.home)
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
	td.Task.Goal = "Goal" + table
	td.Plan = &desktop.Plan{Ref: "p1", Body: "Plan" + table}
	f.task[fleetID] = td
	ad := f.attempt[fleetID]
	ad.Brief = "Brief" + table
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
