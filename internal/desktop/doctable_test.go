package desktop_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

var noControls = []string{"Wrap", "Wide", "Find in file", "Go to line", "Follow", "Rendered", "Source", "Open in browser", "Open as a page"}

func both(t *testing.T, run func(t *testing.T, dark bool)) {
	t.Helper()
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) { run(t, dark) })
	}
}

func noneOf(t *testing.T, tt *ui.Tester, labels ...string) {
	t.Helper()
	for _, s := range labels {
		mustNotText(t, tt, s)
	}
}

func TestACSVOpensAsATableWithOnlyTheControlsThatWork(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		_, tt := openKind(t, kindsFixture(t), salesCSV, dark)
		mustText(t, tt, "Region", "Orders", "Revenue", "Note", "north", "1204", "48210.50", "promo, week 2", "5 rows, 4 columns",
			"Copy path", "Copy contents", "Download", "Table of sales.csv")
		noneOf(t, tt, noControls...)
		noneOf(t, tt, "cannot be shown", "The first")
	})
}

func TestATSVSplitsOnTabsNotCommas(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), salesTSV, false)
	mustText(t, tt, "Region", "Orders", "south", "promo, week 2", "5 rows, 4 columns")
	noneOf(t, tt, "Region\tOrders")
}

func TestACSVDoesNotSplitOnTabs(t *testing.T) {
	f := kindsFixture(t)
	f.docLines["reports/t1/tabs.csv"] = []string{"a\tb,c", "1\t2,3"}
	f.docs["reports/t1/tabs.csv"] = desktop.Doc{Path: "reports/t1/tabs.csv", Name: "tabs.csv", Kind: "csv", Text: true, Size: 12, FolderPage: 1}
	_, tt := openKind(t, f, "reports/t1/tabs.csv", false)
	mustText(t, tt, "a b", "1 2", "1 row, 2 columns")
}

func TestAQuotedNewlineStaysInOneCell(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), quotedCSV, false)
	mustText(t, tt, "id", "note", "first line second line", "says \"hi\", then leaves", "2 rows, 2 columns")
	if hasExact(tt, "first line") || hasExact(tt, "second line\"") {
		t.Fatalf("the quoted newline split the cell in %q", tt.Texts())
	}
}

func TestARaggedFileKeepsEveryRow(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), raggedCSV, false)
	mustText(t, tt, "a", "b", "c", "2 rows, 5 columns", "Table of ragged.csv")
	tt.Frame()
	mustContain(t, tt, "5")
}

func TestAByteOrderMarkIsNotPartOfTheFirstHeader(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), bomCSV, false)
	mustText(t, tt, "name", "count", "alpha")
	for _, s := range tt.Texts() {
		if strings.Contains(s, "\ufeff") {
			t.Fatalf("a byte order mark reached the screen in %q", s)
		}
	}
}

func TestAnEmptyFileSaysSo(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		_, tt := openKind(t, kindsFixture(t), emptyCSV, dark)
		mustText(t, tt, "This file is empty.", "Download")
		noneOf(t, tt, "Table of empty.csv", "rows,")
	})
}

func TestAHeaderOnlyFileSaysThereAreNoRows(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), headerCSV, false)
	mustText(t, tt, "id", "name", "state", "This table has a header and no rows.")
}

func TestAVeryWideCellIsClipped(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), wideCSV, false)
	var cell string
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "0123456789") {
			cell = s
		}
	}
	if cell == "" || !strings.HasSuffix(cell, "…") || strings.Contains(cell, "TAILMARK") || len([]rune(cell)) != 301 {
		t.Fatalf("cell = %q", cell)
	}
}

func TestTheTableStopsAfterTwoThousandRowsAndSaysSo(t *testing.T) {
	const sentence = "The first 2,000 rows are shown. Download saves the whole file."
	for _, tc := range []struct {
		rows int
		cut  bool
	}{{1, false}, {1999, false}, {2000, false}, {2001, true}, {2002, true}, {2500, true}} {
		t.Run(fmt.Sprint(tc.rows), func(t *testing.T) {
			_, tt := openKind(t, eventsFixture(t, tc.rows), bigCSV, false)
			if got := tt.HasText(sentence); got != tc.cut {
				t.Fatalf("%d rows: sentence shown = %v, want %v", tc.rows, got, tc.cut)
			}
			if !tc.cut && !tt.HasText(fmt.Sprintf("%s rows, 3 columns", thousand(tc.rows))) && tc.rows > 1 {
				t.Fatalf("no count line in %q", tt.Texts())
			}
		})
	}
}

func hasExact(tt *ui.Tester, want string) bool {
	return slices.Contains(tt.Texts(), want)
}

func thousand(n int) string {
	s := fmt.Sprint(n)
	if len(s) > 3 {
		return s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s
}

func TestTheLastShownRowIsTheTwoThousandthAndNoMore(t *testing.T) {
	_, tt := openKind(t, eventsFixture(t, 2600), bigCSV, false)
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	mustText(t, tt, "row 2000")
	if hasExact(tt, "row 2001") || hasExact(tt, "row 1") {
		t.Fatalf("rows outside the cap in %q", tt.Texts())
	}
}

func TestABigFileIsReadInWindowsNotWhole(t *testing.T) {
	f := eventsFixture(t, 50000)
	_, tt := openKind(t, f, bigCSV, false)
	mustText(t, tt, "The first 2,000 rows are shown. Download saves the whole file.")
	if f.count("raw:"+bigCSV) != 0 {
		t.Fatal("the whole file was read")
	}
	if f.linesServed > 8*500 {
		t.Fatalf("%d lines read for a table of 2,000 rows", f.linesServed)
	}
}

func TestTheTableTakesTheKeyboardAndScrolls(t *testing.T) {
	_, tt := openKind(t, eventsFixture(t, 300), bigCSV, false)
	if !tt.Focused("Table of events.csv") {
		t.Fatalf("the table has no focus on open: %q", tt.Texts())
	}
	mustText(t, tt, "row 1")
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if !hasExact(tt, "row 300") || hasExact(tt, "row 1") {
		t.Fatalf("End did not reach the last row in %q", tt.Texts())
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if !hasExact(tt, "row 1") || hasExact(tt, "row 300") {
		t.Fatalf("Home did not reach the first row in %q", tt.Texts())
	}
	for range 40 {
		tt.Key(0, ui.KeyDown)
	}
	tt.Frame()
	mustText(t, tt, "row 40")
}

func TestTabReachesTheToolbarThenTheTable(t *testing.T) {
	_, tt := openKind(t, eventsFixture(t, 30), bigCSV, false)
	tt.Key(ui.Shift, ui.KeyTab)
	if !tt.Focused("More document actions") {
		t.Fatalf("Shift+Tab from the table did not reach the document menu")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Table of events.csv") {
		t.Fatalf("Tab from the document menu did not reach the table")
	}
}

func TestOtherKindsShowOneSentenceTheNameAndTheSize(t *testing.T) {
	for _, tc := range []struct{ path, name, size, sentence string }{
		{opusPath, "standup.opus", "3.0 MiB", "This audio file cannot be played in this window. It plays WAV and MP3; download it to play it elsewhere."},
		{clipPath, "demo.mp4", "3.0 MiB", "A video cannot be shown in this window."},
		{planPath, "plan.pdf", "51 KiB", "A PDF cannot be shown in this window."},
		{pagePath, "preview.html", "177 B", "An HTML page cannot be shown in this window."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			both(t, func(t *testing.T, dark bool) {
				_, tt := openKind(t, kindsFixture(t), tc.path, dark)
				mustText(t, tt, tc.name, tc.size, tc.sentence, "Download", "Copy path")
				noneOf(t, tt, "Open in browser", "Open as a page")
				if tc.path != pagePath {
					noneOf(t, tt, "Wrap", "Wide", "Find in file", "Go to line", "Follow", "Rendered", "Source", "Copy contents")
				}
			})
		})
	}
}

func TestAnHTMLPageToggleShowsItsSourceAndItsControls(t *testing.T) {
	app, tt := openKind(t, kindsFixture(t), pagePath, false)
	mustText(t, tt, "Rendered", "Source", "An HTML page cannot be shown in this window.")
	mustContain(t, tt, "<h1>Nightly export</h1>")
	click(t, tt, "Source")
	tt.Frame()
	if !app.DocSource {
		t.Fatal("Source did not turn on")
	}
	mustContain(t, tt, "<h1>Nightly export</h1>", "<script>run()</script>")
	mustText(t, tt, "Wrap", "Wide", "Find in file", "Go to line", "Follow", "Copy contents", "Download")
	noneOf(t, tt, "An HTML page cannot be shown in this window.", "Open in browser")
	click(t, tt, "Rendered")
	tt.Frame()
	mustText(t, tt, "An HTML page cannot be shown in this window.")
}

func TestFindInAnHTMLSourceFindsHits(t *testing.T) {
	_, tt := openKind(t, kindsFixture(t), pagePath, false)
	click(t, tt, "Source")
	tt.Frame()
	search(t, tt, "manifest")
	mustText(t, tt, "1 of 1")
}

func TestNoViewerKindOffersTheBrowser(t *testing.T) {
	for _, path := range []string{salesCSV, salesTSV, emptyCSV, voicePath, clipPath, planPath, pagePath} {
		_, tt := openKind(t, kindsFixture(t), path, false)
		noneOf(t, tt, "Open in browser", "Open as a page", "Open in the Board")
		if got := tt.OpenedURLs(); len(got) != 0 {
			t.Fatalf("%s opened %v", path, got)
		}
	}
}

func TestDownloadWritesTheExactBytesOfTheNewKinds(t *testing.T) {
	for _, rel := range []string{salesCSV, salesTSV, voicePath, clipPath, planPath, pagePath, bomCSV} {
		f := kindsFixture(t)
		app, _ := openKind(t, f, rel, false)
		dest := filepath.Join(t.TempDir(), "saved.out")
		var asked string
		app.SaveAs = func(name string) string { asked = name; return dest }
		tt := sized(t, app, windowWidth, windowHeight, false)
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
	}
}

func TestDownloadStaysEnabledOnEveryNewKind(t *testing.T) {
	for _, rel := range []string{salesCSV, voicePath, clipPath, planPath, pagePath} {
		app, tt := openKind(t, kindsFixture(t), rel, false)
		called := false
		app.SaveAs = func(string) string { called = true; return "" }
		click(t, tt, "Download")
		tt.Frame()
		if !called {
			t.Fatalf("%s: Download did not reach the save hook", rel)
		}
	}
}

func TestCopyContentsOfATableCopiesTheFile(t *testing.T) {
	f := kindsFixture(t)
	_, tt := openKind(t, f, salesCSV, false)
	click(t, tt, "Copy contents")
	tt.Frame()
	if got := tt.Clipboard(); got != salesBody {
		t.Fatalf("clipboard = %q", got)
	}
	mustText(t, tt, "Copied")
}

func TestATableKeepsItsWindowOnReload(t *testing.T) {
	app, tt := openKind(t, kindsFixture(t), salesCSV, false)
	mustText(t, tt, "north")
	app.Retry()
	tt.Frame()
	mustText(t, tt, "north", "5 rows, 4 columns")
}

func TestATableReadErrorShowsTheErrorPage(t *testing.T) {
	f := kindsFixture(t)
	delete(f.docLines, salesCSV)
	_, tt := openKind(t, f, salesCSV, false)
	mustText(t, tt, "Retry")
	noneOf(t, tt, "Table of sales.csv")
}

func TestTheTableKeepsItsHeadAndRowsAtTheNarrowestWindows(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{860, 600}, {600, 500}} {
		for _, dark := range []bool{false, true} {
			app := newApp(t, kindsFixture(t))
			app.OpenDoc(salesCSV)
			tt := sized(t, app, size[0], size[1], dark)
			for _, want := range []string{"sales.csv", "Copy path", "Region", "north"} {
				if _, ok := tt.Find(want); !ok {
					t.Errorf("%vx%v dark=%v: no %q in %q", size[0], size[1], dark, want, tt.Texts())
				}
			}
			tt.Key(0, ui.KeyEnd)
			if _, ok := tt.Find("Region"); !ok {
				t.Errorf("%vx%v dark=%v: header lost after End", size[0], size[1], dark)
			}
		}
	}
}
