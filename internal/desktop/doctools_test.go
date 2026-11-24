package desktop_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	designPath = "reports/t1/design.md"
	notesPath  = "reports/t1/notes.txt"
	blobPath   = "reports/t1/blob.bin"
)

func has(tt *ui.Tester, name string) bool {
	_, ok := tt.Find(name)
	return ok
}

func toolsApp(t *testing.T, rel string) (*desktop.App, *fixture) {
	t.Helper()
	f := docFixture(t)
	app := newApp(t, f)
	app.OpenDoc(rel)
	return app, f
}

func TestSourceShowsTheRawMarkdownAndRenderedTheRendering(t *testing.T) {
	for _, dark := range []bool{false, true} {
		app, _ := toolsApp(t, designPath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "Rendered", "Source", "Goals")
		mustNotText(t, tt, "## Goals")
		click(t, tt, "Source")
		tt.Frame()
		mustText(t, tt, "## Goals", "**Shipped**", "- One job per night that is safe to retry")
		if !app.DocSource || app.DocWrap || app.DocWide {
			t.Fatalf("Source set source %v wrap %v wide %v", app.DocSource, app.DocWrap, app.DocWide)
		}
		click(t, tt, "Source")
		tt.Frame()
		if !app.DocSource {
			t.Fatal("Source toggled itself off")
		}
		click(t, tt, "Rendered")
		tt.Frame()
		mustNotText(t, tt, "## Goals")
		if app.DocSource {
			t.Fatal("Rendered did not turn Source off")
		}
	}
}

func TestToolbarKindsShowOnlyWhatTheyDo(t *testing.T) {
	app, _ := toolsApp(t, notesPath)
	tt := newTester(app)
	mustText(t, tt, "Copy contents", "Download", "Wide", "Wrap")
	mustNotText(t, tt, "Source")
	app.OpenDoc(designPath)
	tt.Frame()
	mustText(t, tt, "Source", "Wide")
	mustNotText(t, tt, "Wrap")
	click(t, tt, "Source")
	tt.Frame()
	mustText(t, tt, "Wrap")
	app.OpenDoc("reports/t1")
	tt.Frame()
	mustNotText(t, tt, "Download")
}

func TestCopyContentsCopiesTheRawTextNotTheRendering(t *testing.T) {
	app, f := toolsApp(t, designPath)
	tt := newTester(app)
	click(t, tt, "Copy contents")
	if got := tt.Clipboard(); got != designMD {
		t.Fatalf("clipboard = %q, want the file as written", got)
	}
	if got := tt.Clipboard(); strings.Contains(got, "Goals\n\nGoals") || !strings.Contains(got, "## Goals") {
		t.Fatalf("clipboard holds rendered text: %q", got)
	}
	tt.Frame()
	mustText(t, tt, "Copied")
	app.OpenDoc(notesPath)
	tt.Frame()
	click(t, tt, "Copy contents")
	if got, want := tt.Clipboard(), string(f.docBytes[notesPath]); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}

func TestCopyContentsRefusesAFileThatIsNotText(t *testing.T) {
	for _, rel := range []string{blobPath, "reports/t1/shot-1.png", "reports/t1/plan.pdf"} {
		app, _ := toolsApp(t, rel)
		tt := newTester(app)
		click(t, tt, "Copy contents")
		tt.Frame()
		if got := tt.Clipboard(); got != "" {
			t.Fatalf("%s: clipboard = %q", rel, got)
		}
		mustText(t, tt, "Copy contents is for text files. This file is not text, so nothing was copied.")
		mustNotText(t, tt, "Copied")
	}
}

func TestDownloadWritesTheExactBytesOfEveryKind(t *testing.T) {
	for _, rel := range []string{blobPath, designPath, notesPath, "reports/t1/shot-1.png"} {
		app, f := toolsApp(t, rel)
		dest := filepath.Join(t.TempDir(), "saved.out")
		var asked string
		app.SaveAs = func(name string) string { asked = name; return dest }
		tt := newTester(app)
		click(t, tt, "Download")
		tt.Frame()
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(got, f.docBytes[rel]) {
			t.Fatalf("%s: wrote % x, want % x", rel, got, f.docBytes[rel])
		}
		if asked != filepath.Base(rel) {
			t.Fatalf("%s: the dialog was offered %q", rel, asked)
		}
		mustText(t, tt, "Saved")
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".hand-download-*")); len(left) > 0 {
			t.Fatalf("temporary files left: %v", left)
		}
	}
}

func TestDownloadCancelledWritesNothingAndSaysNothing(t *testing.T) {
	app, f := toolsApp(t, blobPath)
	app.SaveAs = func(string) string { return "" }
	tt := newTester(app)
	click(t, tt, "Download")
	tt.Frame()
	if n := f.count("stream:" + blobPath); n != 0 {
		t.Fatalf("streamed %d times after Cancel", n)
	}
	mustNotText(t, tt, "Not saved")
	mustNotText(t, tt, "Saved")
}

func TestDownloadFailureKeepsTheExistingFile(t *testing.T) {
	app, f := toolsApp(t, designPath)
	dest := filepath.Join(t.TempDir(), "keep.md")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.SaveAs = func(string) string { return dest }
	f.docErr[designPath] = &desktop.APIError{Status: 500, Code: "failed", Message: "the file could not be read"}
	tt := newTester(app)
	click(t, tt, "Download")
	tt.Frame()
	if b, _ := os.ReadFile(dest); string(b) != "old" {
		t.Fatalf("the file was replaced by %q", b)
	}
	mustText(t, tt, "Not saved")
}

func TestDownloadIsOffWhereThereIsNoSaveDialog(t *testing.T) {
	app, f := toolsApp(t, blobPath)
	tt := newTester(app)
	click(t, tt, "Download")
	tt.Frame()
	if n := f.count("stream:" + blobPath); n != 0 {
		t.Fatalf("streamed %d times", n)
	}
}

func TestWideAndWrapToggleAndAreRemembered(t *testing.T) {
	app, _ := toolsApp(t, notesPath)
	tt := newTester(app)
	if app.DocWide || app.DocWrap {
		t.Fatal("on at the start")
	}
	click(t, tt, "Wide")
	tt.Frame()
	click(t, tt, "Wrap")
	tt.Frame()
	if !app.DocWide || !app.DocWrap {
		t.Fatalf("wide %v wrap %v", app.DocWide, app.DocWrap)
	}
	mustText(t, tt, "Wide", "Wrap")
	if !has(tt, "Wide, on") || !has(tt, "Wrap, on") {
		t.Fatal("the toggles do not say they are on")
	}
	app.Close()

	again := newApp(t, docFixture(t))
	again.DraftDir = app.DraftDir
	again.Boot()
	if !again.DocWide || !again.DocWrap {
		t.Fatalf("not remembered: wide %v wrap %v", again.DocWide, again.DocWrap)
	}
	click(t, tt, "Wide")
	tt.Frame()
	if app.DocWide {
		t.Fatal("Wide did not turn off")
	}
}

func TestWideWidensTheViewerBoxAndWrapBreaksLongLines(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 60)
	app, f := toolsApp(t, notesPath)
	f.docLines[notesPath][0] = long
	tt := sized(t, app, 1500, 800, false)
	narrow, _ := tt.Find("Close the viewer")
	click(t, tt, "Wide")
	tt.Frame()
	wide, _ := tt.Find("Close the viewer")
	if wide.X <= narrow.X {
		t.Fatalf("Wide left the box as it was: close at %v then %v", narrow.X, wide.X)
	}
	click(t, tt, "Wrap")
	tt.Frame()
	if !has(tt, "Text, wrapped") {
		t.Fatal("Wrap did not wrap")
	}
}

func TestToolbarHasAKeyboardPath(t *testing.T) {
	app, _ := toolsApp(t, notesPath)
	dest := filepath.Join(t.TempDir(), "k.txt")
	app.SaveAs = func(string) string { return dest }
	tt := newTester(app)
	order := []string{"Copy contents", "Download", "Wide, off", "Wrap, off"}
	var seen []string
	for range 40 {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		for _, name := range order {
			if tt.Focused(name) {
				seen = append(seen, name)
			}
		}
		if len(seen) == len(order) {
			break
		}
	}
	if strings.Join(seen, "|") != strings.Join(order, "|") {
		t.Fatalf("Tab reached %v, want %v in that order", seen, order)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !app.DocWrap {
		t.Fatal("Enter on Wrap did not toggle it")
	}
	for range 40 {
		tt.Key(ui.Shift, ui.KeyTab)
		tt.Frame()
		if tt.Focused("Download") {
			break
		}
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if b, err := os.ReadFile(dest); err != nil || !strings.Contains(string(b), "line 1") {
		t.Fatalf("Enter on Download wrote %q, %v", b, err)
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeySpace)
	tt.Frame()
}

func TestScenesDocToolbar(t *testing.T) {
	cases := []struct {
		name  string
		rel   string
		w, h  int
		setup func(*desktop.App)
		want  []string
	}{
		{"68-doc-tools-rendered-1100x760", designPath, windowWidth, windowHeight, func(*desktop.App) {}, []string{"Rendered", "Source", "Copy contents", "Download", "Wide"}},
		{"69-doc-tools-source-1100x760", designPath, windowWidth, windowHeight, func(a *desktop.App) { a.DocSource = true }, []string{"## Goals", "Wrap"}},
		{"71-doc-tools-wide-1440x900", designPath, 1440, 900, func(a *desktop.App) { a.DocWide = true }, []string{"Wide"}},
		{"72-doc-tools-binary-1100x760", blobPath, windowWidth, windowHeight, func(*desktop.App) {}, []string{"Download", "Copy contents"}},
	}
	for _, tc := range cases {
		docShots(t, tc.name, tc.w, tc.h, func(app *desktop.App) {
			app.SaveAs = func(string) string { return "" }
			tc.setup(app)
			app.OpenDoc(tc.rel)
		}, func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, tc.want...)
		})
	}
	for _, dark := range []bool{false, true} {
		name := "70-doc-tools-wrap-1100x760-light"
		if dark {
			name = "70-doc-tools-wrap-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := docFixture(t)
			f.docLines[notesPath][1] = strings.Repeat("a long line that has to wrap inside the box ", 8)
			app := newApp(t, f)
			app.SaveAs = func(string) string { return "" }
			app.DocWrap = true
			app.OpenDoc(notesPath)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Wrap")
			if !has(tt, "Text, wrapped") {
				t.Fatal("the lines are not wrapped")
			}
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestToolbarOutcomesAreAnnounced(t *testing.T) {
	app, _ := toolsApp(t, blobPath)
	tt := newTester(app)
	click(t, tt, "Copy contents")
	tt.Frame()
	for _, said := range tt.Announcements() {
		if strings.Contains(said, "not text") {
			return
		}
	}
	t.Fatalf("the refusal was not announced: %q", tt.Announcements())
}

func TestCopyWindowCopiesTheLinesShownOfATextFileOverTenMiB(t *testing.T) {
	app, f := toolsApp(t, "reports/t1/huge.log")
	tt := newTester(app)
	mustText(t, tt, "Copy window")
	mustNotText(t, tt, "Copy contents")
	click(t, tt, "Copy window")
	if got := tt.Clipboard(); got != "alpha\nbeta\ngamma" {
		t.Fatalf("clipboard = %q", got)
	}
	if n := f.count("raw:reports/t1/huge.log"); n != 0 {
		t.Fatalf("read the whole file %d times", n)
	}
}

func TestCopyContentsSaysAKindItDoesNotShowIsNotCopied(t *testing.T) {
	app, _ := toolsApp(t, "reports/t1/table.csv")
	tt := newTester(app)
	click(t, tt, "Copy contents")
	tt.Frame()
	mustText(t, tt, "This kind of file is not copied here, so nothing was copied.")
	mustNotText(t, tt, "not text")
	if tt.Clipboard() != "" {
		t.Fatalf("clipboard = %q", tt.Clipboard())
	}
}

func TestSourceIsRememberedBetweenRuns(t *testing.T) {
	app, _ := toolsApp(t, designPath)
	tt := newTester(app)
	click(t, tt, "Source")
	tt.Frame()
	app.Close()
	again := newApp(t, docFixture(t))
	again.DraftDir = app.DraftDir
	again.Boot()
	if !again.DocSource {
		t.Fatal("Source was not remembered")
	}
}

func TestDownloadedFileIsReadableAndTheButtonWaitsForTheFirstSave(t *testing.T) {
	f := docFixture(t)
	app := newApp(t, f)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	app.OpenDoc(blobPath)
	dest := filepath.Join(t.TempDir(), "saved.bin")
	var calls atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	app.SaveAs = func(string) string {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return dest
	}
	tt := newTester(app)
	drain(posted)
	tt.Frame()
	click(t, tt, "Download")
	<-entered
	tt.Frame()
	click(t, tt, "Download")
	tt.Frame()
	close(release)
	drain(posted)
	if n := calls.Load(); n != 1 {
		t.Fatalf("the save dialog opened %d times", n)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestARepeatedRefusalIsAnnouncedAgain(t *testing.T) {
	app, _ := toolsApp(t, blobPath)
	tt := newTester(app)
	count := func() int {
		n := 0
		for _, said := range tt.Announcements() {
			if strings.Contains(said, "not text") {
				n++
			}
		}
		return n
	}
	click(t, tt, "Copy contents")
	tt.Frame()
	click(t, tt, "Copy contents")
	tt.Frame()
	if n := count(); n != 2 {
		t.Fatalf("the refusal was announced %d times, want 2: %q", n, tt.Announcements())
	}
}

type gatedCopy struct {
	*fixture
	entered chan struct{}
	release chan struct{}
}

func (g *gatedCopy) DocBytes(ctx context.Context, id, path string) ([]byte, error) {
	b, err := g.fixture.DocBytes(ctx, id, path)
	g.entered <- struct{}{}
	<-g.release
	return b, err
}

func TestACopyFromAnEarlierVisitDoesNotLandOnTheNewVisit(t *testing.T) {
	f := docFixture(t)
	g := &gatedCopy{fixture: f, entered: make(chan struct{}, 4), release: make(chan struct{})}
	app := desktop.New(func() (desktop.ReadModel, error) { return g, nil })
	app.DraftDir = t.TempDir()
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.Boot()
	drain(posted)
	app.OpenDoc(notesPath)
	tt := newTester(app)
	drain(posted)
	tt.Frame()
	click(t, tt, "Copy contents")
	<-g.entered
	app.OpenDoc(designPath)
	drain(posted)
	app.OpenDoc(notesPath)
	drain(posted)
	tt.Frame()
	close(g.release)
	drain(posted)
	tt.Frame()
	if got := tt.Clipboard(); got != "" {
		t.Fatalf("a copy from the first visit landed: %q", got)
	}
	mustNotText(t, tt, "Copied")
}
