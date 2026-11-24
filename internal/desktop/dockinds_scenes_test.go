package desktop_test

import (
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

func openKind(t *testing.T, f *fixture, path string, dark bool) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := newApp(t, f)
	app.SaveAs = func(string) string { return "" }
	app.OpenDoc(path)
	return app, sized(t, app, windowWidth, windowHeight, dark)
}

func inked(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.W == 0 {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	img := tt.Image()
	rect := image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)).Intersect(img.Bounds())
	bg := color.NRGBAModel.Convert(img.At(rect.Min.X-4, rect.Min.Y+rect.Dy()/2)).(color.NRGBA)
	base := ui.Color{R: bg.R, G: bg.G, B: bg.B}
	if ink := rect.Dx()*rect.Dy() - countNear(img, rect, base, 70); ink < 12 {
		t.Errorf("%q is drawn blank: %d inked pixels in %v", label, ink, rect)
	}
}

func kindScene(t *testing.T, base, path string, mk func(t *testing.T) *fixture,
	drive func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool)) {
	t.Helper()
	pinZone(t)
	for _, dark := range []bool{false, true} {
		name := base + "-1100x760-light"
		if dark {
			name = base + "-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app, tt := openKind(t, mk(t), path, dark)
			drive(t, app, tt, dark)
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func mustContain(t *testing.T, tt *ui.Tester, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("missing %q in %q", w, tt.Texts())
		}
	}
}

func notice(t *testing.T, tt *ui.Tester, dark bool, sentence string) {
	t.Helper()
	mustText(t, tt, sentence, "Download", "Copy path")
	if !hasRun(tt.Image(), kit.ColorsFor(dark).WaitBG, 50) {
		t.Error("the notice has no ground")
	}
	inked(t, tt, sentence)
	inked(t, tt, "Download")
}

func TestScenesTheDocumentTable(t *testing.T) {
	kindScene(t, "122-doc-table-csv", salesCSV, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "sales.csv", "Region", "Orders", "Revenue", "Note", "north", "1204", "48210.50", "promo, week 2",
			"5 rows, 4 columns", "Copy contents", "Download")
		mustNotText(t, tt, "The first")
		for _, s := range []string{"Region", "north", "48210.50", "promo, week 2", "5 rows, 4 columns"} {
			inked(t, tt, s)
		}
	})
}

func TestScenesTheDocumentTableOfTabs(t *testing.T) {
	kindScene(t, "123-doc-table-tsv", salesTSV, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "sales.tsv", "Region", "Revenue", "south", "39120.00", "promo, week 2", "5 rows, 4 columns")
		for _, s := range []string{"Region", "south", "promo, week 2"} {
			inked(t, tt, s)
		}
	})
}

func TestScenesTheDocumentTableTruncated(t *testing.T) {
	kindScene(t, "124-doc-table-truncated", bigCSV, func(t *testing.T) *fixture { return eventsFixture(t, 2500) },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			mustText(t, tt, "events.csv", "id", "kind", "detail", "row 1", "The first 2,000 rows are shown. Download saves the whole file.")
			inked(t, tt, "The first 2,000 rows are shown. Download saves the whole file.")
			inked(t, tt, "row 1")
		})
}

func TestScenesAnAudioFileHasNoPlayer(t *testing.T) {
	kindScene(t, "125-doc-kind-audio", voicePath, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "standup.mp3", "3.0 MiB")
		notice(t, tt, dark, "An audio file cannot be shown in this window.")
	})
}

func TestScenesAVideoHasNoPlayer(t *testing.T) {
	kindScene(t, "126-doc-kind-video", clipPath, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "demo.mp4", "3.0 MiB")
		notice(t, tt, dark, "A video cannot be shown in this window.")
	})
}

func TestScenesAPDFIsNotDrawn(t *testing.T) {
	kindScene(t, "127-doc-kind-pdf", planPath, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "plan.pdf", "51 KiB")
		notice(t, tt, dark, "A PDF cannot be shown in this window.")
	})
}

func TestScenesAnHTMLPageShowsItsSource(t *testing.T) {
	kindScene(t, "128-doc-kind-html-source", pagePath, kindsFixture, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "An HTML page cannot be shown in this window.", "Rendered", "Source")
		click(t, tt, "Source")
		tt.Frame()
		mustContain(t, tt, "<h1>Nightly export</h1>", "<script>run()</script>")
		mustText(t, tt, "Wrap", "Go to line", "Follow")
		mustNotText(t, tt, "An HTML page cannot be shown in this window.")
		inked(t, tt, "Source")
		inked(t, tt, pageSource)
	})
}
