package desktop_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

const docRefusedWords = "the Board does not show Hand's own files, dot files, backups/, cache/, luvus/, locks/ or projects/"

func hasRun(img *image.RGBA, want ui.Color, min int) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		n := 0
		for x := b.Min.X; x < b.Max.X; x++ {
			if p := img.RGBAAt(x, y); rgbIs(p, want) {
				if n++; n >= min {
					return true
				}
			} else {
				n = 0
			}
		}
	}
	return false
}

func docShots(t *testing.T, name string, width, height int, open func(*desktop.App), check func(*testing.T, *ui.Tester, bool)) {
	t.Helper()
	for _, dark := range []bool{false, true} {
		shotName := name + "-light"
		if dark {
			shotName = name + "-dark"
		}
		t.Run(shotName, func(t *testing.T) {
			app := newApp(t, docFixture(t))
			open(app)
			tt := sized(t, app, width, height, dark)
			check(t, tt, dark)
			shotSized(t, tt, shotName, width, height)
		})
	}
}

func TestScenesTheMarkdownDocument(t *testing.T) {
	docShots(t, "55-doc-markdown-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) { a.OpenDoc("reports/t1/design.md") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "Fleets / golden /", "reports", "t1", "design.md", "reports/t1/design.md", "Copy path",
				"2 KiB", "Next →", "notes.txt", "Nightly export pipeline: design", "Goals", "A manifest written last",
				"Open as a page")
			mustNotText(t, tt, "← Previous")
			if got := tt.Image().RGBAAt(500, 600); !rgbIs(got, kit.ColorsFor(dark).BG) {
				t.Errorf("the page under the text is %v, want the ground", got)
			}
		})
}

func TestScenesTheFolderListing(t *testing.T) {
	docShots(t, "56-doc-folder-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) { a.OpenDoc("reports/t1") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "Fleets / golden /", "t1/", "Copy path", "8 entries, page 1 of 2",
				"sub/", "design.md", "notes.txt", "plan.pdf", "DIR", "MD", "TXT", "PDF", "2 KiB · ", "Next")
			mustNotText(t, tt, "Previous")
			mustNotText(t, tt, "shot-1.png")
			if !hasRun(tt.Image(), kit.ColorsFor(dark).Line, 600) {
				t.Error("the listing has no hairline under its rows")
			}
		})
}

func TestScenesTheFolderOnItsSecondPage(t *testing.T) {
	docShots(t, "57-doc-folder-page-2-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) { a.OpenDoc("reports/t1") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			click(t, tt, "Next")
			mustText(t, tt, "8 entries, page 2 of 2", "shot-1.png", "z-last.txt", "PNG", "Previous")
			mustNotText(t, tt, "design.md")
			mustNotText(t, tt, "Next")
		})
}

func TestTheFolderPagerWalksThePagesAndBackKeepsTheOne(t *testing.T) {
	f := docFixture(t)
	app := newApp(t, f)
	app.OpenDoc("reports/t1")
	tt := newTester(app)
	mustText(t, tt, "page 1 of 2")
	click(t, tt, "Next")
	mustText(t, tt, "page 2 of 2", "shot-1.png")
	click(t, tt, "Previous")
	mustText(t, tt, "page 1 of 2", "design.md")
	click(t, tt, "Next")
	click(t, tt, "shot-1.png")
	if app.Ref != "reports/t1/shot-1.png" {
		t.Fatalf("opened %q", app.Ref)
	}
	mustText(t, tt, "Fleets / golden /", "shot-1.png")
	click(t, tt, "Back")
	mustText(t, tt, "page 2 of 2", "shot-1.png", "z-last.txt")
	if f.count("doc:reports/t1") == 0 {
		t.Fatal("the listing was never read")
	}
}

func TestAFileLinksBackToItsFoldersPage(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenDoc("reports/t1/shot-2.png")
	tt := newTester(app)
	click(t, tt, "folder reports/t1")
	if app.Ref != "reports/t1" {
		t.Fatalf("the crumb opened %q", app.Ref)
	}
	mustText(t, tt, "page 2 of 2", "shot-2.png")
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	click(t, tt, "folder reports/t1")
	mustText(t, tt, "page 1 of 2", "design.md")
}

func TestBackGoesThroughTheFolderThenTheFile(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenRef("t1")
	tt := newTester(app)
	app.OpenDoc("reports/t1")
	tt.Frame()
	click(t, tt, "sub/")
	if app.Ref != "reports/t1/sub" {
		t.Fatalf("opened %q", app.Ref)
	}
	click(t, tt, "deep.md")
	mustText(t, tt, "Export notes", "The old export is retired.")
	if app.Ref != "reports/t1/sub/deep.md" {
		t.Fatalf("opened %q", app.Ref)
	}
	for _, want := range []string{"reports/t1/sub", "reports/t1"} {
		click(t, tt, "Back")
		if app.Screen != desktop.DocScreen || app.Ref != want {
			t.Fatalf("Back left %v %q, want %q", app.Screen, app.Ref, want)
		}
		if desktop.DocShown(app) != want {
			t.Fatalf("Back showed %q, want %q", desktop.DocShown(app), want)
		}
	}
	mustText(t, tt, "page 1 of 2", "design.md")
	click(t, tt, "Back")
	if app.Screen != desktop.TaskScreen || app.Ref != "t1" {
		t.Fatalf("the last Back left %v %q", app.Screen, app.Ref)
	}
	app.OpenDoc("reports/t1/design.md")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Escape left %v", app.Screen)
	}
	if desktop.DocShown(app) != "" {
		t.Fatal("a closed viewer still holds the document")
	}
}

func TestTheTextWindowsReadThroughNextAndNeverTheWholeFile(t *testing.T) {
	f := docFixture(t)
	app := newApp(t, f)
	app.OpenDoc("reports/t1/notes.txt")
	tt := newTester(app)
	mustText(t, tt, "line 1 of the export log", "line 3 of the export log", "Later", "From line 1 of a 120 B file")
	mustNotText(t, tt, "Earlier")
	mustNotText(t, tt, "line 5 of the export log")
	click(t, tt, "Later")
	mustText(t, tt, "From line 4 of a 120 B file", "indented", "line 5 of the export log", "Earlier")
	mustNotText(t, tt, "Later")
	mustNotText(t, tt, "line 1 of the export log")
	for _, s := range tt.Texts() {
		if strings.Contains(s, "\t") {
			t.Fatalf("a tab reached the screen: %q", s)
		}
	}
	click(t, tt, "Earlier")
	mustText(t, tt, "line 1 of the export log", "Later")
	if n := f.count("raw:reports/t1/notes.txt"); n != 0 {
		t.Fatalf("the text was read whole %d times", n)
	}
	if f.count("window:reports/t1/notes.txt:1") != 2 || f.count("window:reports/t1/notes.txt:4") != 1 {
		t.Fatalf("windows read: %v", f.calls)
	}
}

func TestScenesTheTextWithLineNumbers(t *testing.T) {
	for _, second := range []bool{false, true} {
		name := "58-doc-text-window-1-1100x760"
		if second {
			name = "59-doc-text-window-2-1100x760"
		}
		docShots(t, name, windowWidth, windowHeight,
			func(a *desktop.App) { a.OpenDoc("reports/t1/notes.txt") },
			func(t *testing.T, tt *ui.Tester, dark bool) {
				if second {
					click(t, tt, "Later")
					mustText(t, tt, "From line 4 of a 120 B file", "line 5 of the export log")
				} else {
					mustText(t, tt, "From line 1 of a 120 B file", "line 3 of the export log")
				}
				mustText(t, tt, "← Previous", "design.md", "Next →", "plan.pdf", "Copy path")
				if !hasRun(tt.Image(), kit.ColorsFor(dark).Card, 50) {
					t.Error("the lines have no card ground")
				}
			})
	}
}

func TestAnImageDrawsItsRawBytes(t *testing.T) {
	f := docFixture(t)
	app := newApp(t, f)
	app.OpenDoc("reports/t1/shot-1.png")
	for _, dark := range []bool{false, true} {
		name := "60-doc-image-1100x760-light"
		if dark {
			name = "60-doc-image-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "shot-1.png", "Next →", "shot-2.png")
			mustNotText(t, tt, "← Previous")
			img := tt.Image()
			for _, tone := range []color.RGBA{{0x40, 0x7a, 0xcc, 255}, {0xc4, 0x2b, 0x32, 255}, {0x1a, 0x7a, 0x3e, 255}} {
				if !hasRun(img, ui.RGBA(tone.R, tone.G, tone.B, 1), 50) {
					t.Errorf("no run of %v in the picture", tone)
				}
			}
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
	if f.count("raw:reports/t1/shot-1.png") == 0 {
		t.Fatal("the picture's bytes were never read")
	}
}

func TestPreviousAndNextStepThroughTheFolder(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	mustNotText(t, tt, "← Previous")
	click(t, tt, "Next →")
	if app.Ref != "reports/t1/notes.txt" {
		t.Fatalf("Next opened %q", app.Ref)
	}
	mustText(t, tt, "← Previous", "Next →", "line 1 of the export log")
	click(t, tt, "← Previous")
	if app.Ref != "reports/t1/design.md" {
		t.Fatalf("Previous opened %q", app.Ref)
	}
	mustText(t, tt, "Nightly export pipeline: design")
	app.OpenDoc("reports/t1/z-last.txt")
	tt.Frame()
	mustText(t, tt, "← Previous", "shot-3.png", "the end")
	mustNotText(t, tt, "Next →")
	click(t, tt, "Back")
	if app.Ref != "reports/t1/design.md" {
		t.Fatalf("stepping left %q on the stack, want the page before", app.Ref)
	}
}

func TestAnImagesPreviousAndNextSkipWhatIsNotAnImage(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenDoc("reports/t1/shot-1.png")
	tt := newTester(app)
	mustNotText(t, tt, "← Previous")
	click(t, tt, "Next →")
	click(t, tt, "Next →")
	if app.Ref != "reports/t1/shot-3.png" {
		t.Fatalf("opened %q", app.Ref)
	}
	mustText(t, tt, "← Previous", "shot-2.png")
	mustNotText(t, tt, "Next →")
	mustNotText(t, tt, "z-last.txt")
	click(t, tt, "← Previous")
	if app.Ref != "reports/t1/shot-2.png" {
		t.Fatalf("Previous opened %q", app.Ref)
	}
}

func TestScenesAKindThatOpensInTheBrowser(t *testing.T) {
	docShots(t, "61-doc-browser-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) { a.OpenDoc("reports/t1/plan.pdf") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "plan.pdf", "A PDF opens in the Board's viewer, in the browser.", "Open in browser", "Copy path")
			if !hasRun(tt.Image(), kit.ColorsFor(dark).WaitBG, 50) {
				t.Error("the row has no notice ground")
			}
		})
}

func TestEveryOtherKindHasARowThatOpensTheBoard(t *testing.T) {
	hand := installFakeHand(t)
	f := docFixture(t)
	f.fleets[0].Home = hand.home
	for kind, row := range map[string]string{
		"pdf": "A PDF opens", "html": "An HTML page opens", "svg": "An SVG picture opens", "csv": "A CSV table opens",
		"tsv": "A TSV table opens", "video": "A video opens", "audio": "An audio file opens",
		"binary": "This file is not text, so the Board does not show it; download it to open it.",
		"future": "This file opens",
	} {
		path := "reports/t1/x." + kind
		f.docs[path] = desktop.Doc{Path: path, Name: "x." + kind, Kind: kind, Size: 9, At: at("1"), FolderPage: 1}
		t.Run(kind, func(t *testing.T) {
			hand.reset(t)
			app := newApp(t, f)
			app.OpenDoc(path)
			tt := newTester(app)
			mustText(t, tt, row, "Open in browser")
			click(t, tt, "Open in browser")
			hand.want(t, "open", "--doc", path)
		})
	}
}

func TestAMarkdownThatIsNotTextOpensInTheBrowser(t *testing.T) {
	f := docFixture(t)
	f.docBytes["reports/t1/design.md"] = []byte("# Title\n\xff\xfe broken")
	app := newApp(t, f)
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	mustText(t, tt, "This file is not text, so the Board does not show it; download it to open it.", "Open in browser")
	mustNotText(t, tt, "Title")
}

func TestAPictureTooLargeOrBrokenOpensInTheBrowser(t *testing.T) {
	f := docFixture(t)
	big := f.docs["reports/t1/shot-1.png"]
	big.Size = 11 << 20
	f.docs["reports/t1/shot-1.png"] = big
	f.docBytes["reports/t1/shot-2.png"] = []byte("not a picture")
	f.docBytes["reports/t1/shot-3.png"] = hugePNG(40000, 40000)
	app := newApp(t, f)
	app.OpenDoc("reports/t1/shot-1.png")
	tt := newTester(app)
	mustText(t, tt, "This picture is larger than this window reads.", "Open in browser")
	if f.count("raw:reports/t1/shot-1.png") != 0 {
		t.Fatal("a picture over the limit was read")
	}
	click(t, tt, "Next →")
	mustText(t, tt, "This picture could not be drawn here.", "Open in browser")
	click(t, tt, "Next →")
	mustText(t, tt, "This picture is larger than this window reads.", "Open in browser")
}

func TestScenesARefusedPath(t *testing.T) {
	docShots(t, "62-doc-refused-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) {
			f := a.Model.(*fixture)
			f.docErr["backups/db.sqlite"] = &desktop.APIError{Status: 403, Code: "forbidden", Message: "the Board does not show Hand's own files, dot files, backups/, cache/, luvus/, locks/ or projects/"}
			a.OpenDoc("backups/db.sqlite")
		},
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "backups/db.sqlite", "Open in browser", docRefusedWords)
			mustNotText(t, tt, "The page could not be read")
			mustNotText(t, tt, "Retry")
			mustNotText(t, tt, "the board's API answers only on its loopback address")
		})
}

func TestAMissingPathAndAChangedFileUseTheBoardsWords(t *testing.T) {
	f := docFixture(t)
	f.docErr["reports/t1/moving.md"] = &desktop.APIError{Status: 409, Code: "conflict", Message: "this file changed while the Board opened it; reload the page"}
	app := newApp(t, f)
	app.OpenDoc("reports/t1/gone.md")
	tt := newTester(app)
	mustText(t, tt, "there is no file or folder at this path", "Open in browser")
	mustNotText(t, tt, "Retry")
	app.OpenDoc("reports/t1/moving.md")
	tt.Frame()
	mustText(t, tt, "this file changed while the Board opened it; reload the page", "Retry")
	delete(f.docErr, "reports/t1/moving.md")
	f.docs["reports/t1/moving.md"] = desktop.Doc{Path: "reports/t1/moving.md", Name: "moving.md", Kind: "markdown", Text: true, Size: 5, At: at("1"), FolderPage: 1}
	f.docBytes["reports/t1/moving.md"] = []byte("# Settled\n")
	click(t, tt, "Retry")
	mustText(t, tt, "Settled")
	mustNotText(t, tt, "Retry")
}

func TestScenesAnErrorWithRetry(t *testing.T) {
	docShots(t, "63-doc-error-1100x760", windowWidth, windowHeight,
		func(a *desktop.App) { a.OpenDoc("reports/t1/flaky.md") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "The page could not be read", "the connection was reset", "Retry", "Close")
		})
}

func TestRetryReadsTheDocumentAgain(t *testing.T) {
	f := docFixture(t)
	app := newApp(t, f)
	app.OpenDoc("reports/t1/flaky.md")
	tt := newTester(app)
	mustText(t, tt, "the connection was reset")
	click(t, tt, "Retry")
	mustText(t, tt, "the connection was reset")
	delete(f.docErr, "reports/t1/flaky.md")
	click(t, tt, "Retry")
	mustText(t, tt, "Steady now")
	mustNotText(t, tt, "The page could not be read")
}

func TestALongLineScrollsInsteadOfWideningTheBox(t *testing.T) {
	f := docFixture(t)
	f.docLines["reports/t1/z-last.txt"] = []string{strings.Repeat("long ", 200), "short"}
	app := newApp(t, f)
	app.OpenDoc("reports/t1/z-last.txt")
	tt := newTester(app)
	r, ok := tt.Find("Close the viewer")
	if !ok || int(r.X+r.W) != 1067 {
		t.Fatalf("the box's right edge is %v, want 1076", r)
	}
	if !hasRun(tt.Image(), kit.ColorsFor(false).Card, 50) {
		t.Error("the lines have no card ground")
	}
	img := tt.Image()
	if got := img.RGBAAt(1085, 300); got.R > 200 {
		t.Errorf("right of the box is %v, want the backdrop", got)
	}
}

type slowDoc struct {
	desktop.ReadModel
	gate    string
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	held    bool
}

func (s *slowDoc) Doc(ctx context.Context, fleet, path string, page int) (desktop.Doc, error) {
	s.mu.Lock()
	hold := path == s.gate && !s.held
	s.held = s.held || hold
	s.mu.Unlock()
	if hold {
		s.entered <- struct{}{}
		<-s.release
	}
	return s.ReadModel.Doc(ctx, fleet, path, page)
}

func drain(posted <-chan func()) {
	for {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

func slowDocApp(t *testing.T, gate string) (*desktop.App, *slowDoc, chan func()) {
	t.Helper()
	model := &slowDoc{ReadModel: docFixture(t), gate: gate, entered: make(chan struct{}, 1), release: make(chan struct{})}
	app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	return app, model, posted
}

func waitEntered(t *testing.T, model *slowDoc) {
	t.Helper()
	select {
	case <-model.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow read never started")
	}
}

func TestScenesASlowAnswerOvertakenByANewerOne(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "64-doc-overtaken-1100x760-light"
		if dark {
			name = "64-doc-overtaken-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app, model, posted := slowDocApp(t, "reports/t1/design.md")
			app.OpenDoc("reports/t1/design.md")
			waitEntered(t, model)
			app.OpenDoc("reports/t1/sub/deep.md")
			waitPost(t, posted)()
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "deep.md", "Export notes")
			close(model.release)
			drain(posted)
			tt.Frame()
			mustText(t, tt, "deep.md", "Export notes")
			mustNotText(t, tt, "Nightly export pipeline: design")
			if desktop.DocShown(app) != "reports/t1/sub/deep.md" {
				t.Fatalf("the view shows %q", desktop.DocShown(app))
			}
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestAnAnswerIsDroppedWhenTheViewerMovedOn(t *testing.T) {
	for _, tc := range []struct {
		name string
		move func(*desktop.App)
		ref  string
	}{
		{"another document", func(a *desktop.App) { a.OpenDoc("reports/t1/sub") }, "reports/t1/sub"},
		{"next", func(a *desktop.App) { a.OpenDoc("reports/t1/z-last.txt") }, "reports/t1/z-last.txt"},
		{"back", func(a *desktop.App) { a.Back() }, ""},
		{"close", func(a *desktop.App) { a.CloseViewer() }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, model, posted := slowDocApp(t, "reports/t1/design.md")
			app.Post = nil
			app.OpenRef("t1")
			app.Post = func(fn func()) { posted <- fn }
			app.OpenDoc("reports/t1/design.md")
			waitEntered(t, model)
			app.Post = nil
			tc.move(app)
			app.Post = func(fn func()) { posted <- fn }
			close(model.release)
			drain(posted)
			if got := desktop.DocShown(app); got == "reports/t1/design.md" {
				t.Fatalf("the older answer for %q was kept", got)
			}
			if tc.ref != "" && desktop.DocShown(app) != tc.ref {
				t.Fatalf("shows %q, want %q", desktop.DocShown(app), tc.ref)
			}
		})
	}
}

func TestCopyPathCopiesTheFleetRelativePath(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	click(t, tt, "Copy path")
	if got := tt.Clipboard(); got != "reports/t1/design.md" {
		t.Fatalf("clipboard = %q", got)
	}
	tt.Frame()
	mustText(t, tt, "Copied")
	app.OpenDoc("reports/t1")
	tt.Frame()
	click(t, tt, "Copy path")
	if got := tt.Clipboard(); got != "reports/t1" || strings.HasPrefix(got, "/") {
		t.Fatalf("clipboard = %q", got)
	}
}

func TestAMarkdownLinkOpensTheDocumentItNames(t *testing.T) {
	app := newApp(t, docFixture(t))
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	click(t, tt, "API notes")
	if app.Ref != "reports/t1/notes.txt" {
		t.Fatalf("a relative link opened %q", app.Ref)
	}
	click(t, tt, "Back")
	click(t, tt, "export notes")
	if app.Ref != "reports/t1/sub/deep.md" {
		t.Fatalf("a Board link opened %q", app.Ref)
	}
	f := docFixture(t)
	f.docBytes["reports/t1/design.md"] = []byte("[up](../../../secret.md) and [far](../../../../x.md) and [in](sub/deep.md)\n")
	app = newApp(t, f)
	app.OpenDoc("reports/t1/design.md")
	tt = newTester(app)
	click(t, tt, "up")
	click(t, tt, "far")
	if app.Ref != "reports/t1/design.md" {
		t.Fatalf("a link out of the fleet opened %q", app.Ref)
	}
	click(t, tt, "in")
	if app.Ref != "reports/t1/sub/deep.md" {
		t.Fatalf("a link into a folder opened %q", app.Ref)
	}
}

func TestADocumentNeverShowsAHostPath(t *testing.T) {
	app := newApp(t, docFixture(t))
	for _, path := range []string{"reports/t1/design.md", "reports/t1", "reports/t1/notes.txt"} {
		app.OpenDoc(path)
		tt := newTester(app)
		for _, s := range tt.Texts() {
			if strings.Contains(s, "/home/") || strings.Contains(s, "/Users/") || strings.Contains(s, `C:\`) {
				t.Fatalf("%s shows a host path: %q", path, s)
			}
		}
	}
}

func TestScenesTheDocumentInANarrowWindow(t *testing.T) {
	docShots(t, "65-doc-markdown-880x760", 880, 760,
		func(a *desktop.App) { a.OpenDoc("reports/t1/design.md") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "design.md", "Copy path", "Next →", "Nightly export pipeline: design")
			r, ok := tt.Find("Close the viewer")
			if !ok || int(r.X+r.W) > 880-24 {
				t.Errorf("the close button %v is outside the box", r)
			}
		})
	docShots(t, "66-doc-folder-880x760", 880, 760,
		func(a *desktop.App) { a.OpenDoc("reports/t1") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "t1/", "Copy path", "8 entries, page 1 of 2", "design.md", "Next")
		})
	docShots(t, "67-doc-text-880x760", 880, 760,
		func(a *desktop.App) { a.OpenDoc("reports/t1/notes.txt") },
		func(t *testing.T, tt *ui.Tester, dark bool) {
			mustText(t, tt, "notes.txt", "From line 1 of a 120 B file", "line 1 of the export log")
		})
}

func TestAnEmptyFolderAndAnEmptyFile(t *testing.T) {
	f := docFixture(t)
	f.docs["reports/t1/empty"] = desktop.Doc{Path: "reports/t1/empty", Name: "empty", Kind: "folder", At: at("1"), Page: 1, PageSize: 4, Total: 0, Pages: 1}
	f.docs["reports/t1/empty.txt"] = desktop.Doc{Path: "reports/t1/empty.txt", Name: "empty.txt", Kind: "text", Text: true, At: at("1"), FolderPage: 1}
	f.docLines["reports/t1/empty.txt"] = nil
	app := newApp(t, f)
	app.OpenDoc("reports/t1/empty")
	tt := newTester(app)
	mustText(t, tt, "This folder is empty.")
	app.OpenDoc("reports/t1/empty.txt")
	tt.Frame()
	mustText(t, tt, "This file is empty.", "0 B")
}

func TestARefusedFolderEntryTakesNoClick(t *testing.T) {
	f := docFixture(t)
	d := f.docs["reports/t1"]
	d.Entries = append([]desktop.DocEntry{{Name: "out", Path: "reports/t1/out", Kind: "link", Label: "LINK", Refused: "leads outside the fleet folder"}}, d.Entries...)
	f.docs["reports/t1"] = d
	app := newApp(t, f)
	app.OpenDoc("reports/t1")
	tt := newTester(app)
	mustText(t, tt, "out", "LINK", "leads outside the fleet folder")
	if err := tt.Click("out"); err == nil {
		tt.Frame()
	}
	if app.Ref != "reports/t1" {
		t.Fatalf("a refused link opened %q", app.Ref)
	}
}

func TestAnUnreadableAnswerKeepsTheViewerUsable(t *testing.T) {
	f := docFixture(t)
	f.docErr["reports/t1/design.md"] = context.DeadlineExceeded
	app := newApp(t, f)
	app.OpenRef("t1")
	app.OpenDoc("reports/t1/design.md")
	tt := newTester(app)
	mustText(t, tt, "the board did not answer in time", "Retry")
	click(t, tt, "Close")
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("Close left %v", app.Screen)
	}
	if !errors.Is(f.docErr["reports/t1/design.md"], context.DeadlineExceeded) {
		t.Fatal("fixture changed")
	}
}

func hugePNG(w, h uint32) []byte {
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, w), h)
	ihdr = append(ihdr, 8, 0, 0, 0, 0)
	binary.Write(&out, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	out.Write(chunk)
	binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return out.Bytes()
}
