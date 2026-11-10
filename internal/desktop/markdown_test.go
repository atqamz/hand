package desktop_test

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo/ui"
)

const boardBase = "http://127.0.0.1:7777"

type boarded struct{ *fixture }

func (boarded) BoardBase() string { return boardBase }

const reportBody = "Result:\n\n| Check | Outcome |\n|---|---|\n| lint | green |\n| tests | green |\n\n```go\nfmt.Println(\"hi\")\n```\n\n- [x] shipped\n- [ ] announced\n\n"

func reportApp(t *testing.T, body string) (*desktop.App, *ui.Tester) {
	t.Helper()
	f := goldenFixture(t)
	rd := f.report[fleetID]
	rd.Report.Body = body
	f.report[fleetID] = rd
	app := desktop.New(func() (desktop.ReadModel, error) { return boarded{f}, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	tt := newTester(app)
	click(t, tt, "Needs")
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
		name, dest, want string
	}{
		{"https", "https://github.com/atqamz/hand/pull/1", "https://github.com/atqamz/hand/pull/1"},
		{"http", "http://example.com/a", "http://example.com/a"},
		{"fleet path", "/" + fleetID + "/doc/reports/t1/a.md", boardBase + "/" + fleetID + "/doc/reports/t1/a.md"},
		{"other fleet path", "/" + secondID + "/doc/a.md", ""},
		{"board root", "/", ""},
		{"traversal", "/" + fleetID + "/doc/../../etc/passwd", ""},
		{"protocol relative", "//evil.example/" + fleetID + "/", ""},
		{"javascript", "javascript:alert(1)", ""},
		{"file", "file:///etc/passwd", ""},
		{"mailto", "mailto:a@example.com", ""},
		{"relative", "reports/t1/a.md", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, tt := reportApp(t, "Open [the link]("+tc.dest+") now")
			click(t, tt, "the link")
			got := tt.OpenedURLs()
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("opened %q", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("opened %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlainTextSelectsAndCopies(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	line := "agent " + app.Status.AgentState
	r, ok := tt.Find(line)
	if !ok {
		t.Fatalf("status line %q not on screen", line)
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
				click(t, tt, "Timeline")
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
