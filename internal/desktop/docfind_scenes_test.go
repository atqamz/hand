package desktop_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func pinZone(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
}

func docScene(t *testing.T, base, path string, drive func(t *testing.T, f *fixture, tt *ui.Tester)) {
	t.Helper()
	pinZone(t)
	for _, dark := range []bool{false, true} {
		name := base + "-1100x760-light"
		if dark {
			name = base + "-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			f := findFixture(t)
			_, tt := openFind(t, f, path, dark)
			drive(t, f, tt)
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func TestScenesTheDocumentFindShowsHits(t *testing.T) {
	docScene(t, "103-doc-find", bigLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		search(t, tt, "error")
		click(t, tt, "Next match")
		mustText(t, tt, "2 of 5+", "Line 142")
	})
}

func TestScenesTheDocumentFindWithNoMatch(t *testing.T) {
	docScene(t, "104-doc-find-none", bigLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		search(t, tt, "unicorn")
		mustText(t, tt, "not in this file")
	})
}

func TestScenesTheDocumentGoToLine(t *testing.T) {
	docScene(t, "105-doc-goto-line", bigLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		click(t, tt, "Go to line")
		tt.Type("60000")
		click(t, tt, "Go")
		mustText(t, tt, "Line 60000", "From line 59980")
	})
}

func TestScenesTheDocumentGoToLineOutOfRange(t *testing.T) {
	docScene(t, "106-doc-goto-line-range", bigLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		click(t, tt, "Go to line")
		tt.Type("100001")
		click(t, tt, "Go")
		mustText(t, tt, "This file has no line 100,001; it ends at line 100,000.")
	})
}

func TestScenesTheDocumentFollowsTheEnd(t *testing.T) {
	docScene(t, "107-doc-follow", liveLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		click(t, tt, "Follow")
		f.grow(liveLog, "tick 0801", "tick 0802")
		tt.Advance(2500 * time.Millisecond)
		tt.Frame()
		mustText(t, tt, "Following", "tick 0802")
	})
}

func TestScenesTheDocumentFollowStopsOnScrollUp(t *testing.T) {
	docScene(t, "108-doc-follow-stopped", liveLog, func(t *testing.T, f *fixture, tt *ui.Tester) {
		click(t, tt, "Follow")
		tt.Scroll(500, 400, 0, -3000)
		tt.Frame()
		mustText(t, tt, "Follow stopped: you scrolled up.")
	})
}

func TestScenesTheDocumentFindInMarkdownSource(t *testing.T) {
	docScene(t, "109-doc-find-markdown-source", "reports/t1/design.md", func(t *testing.T, f *fixture, tt *ui.Tester) {
		click(t, tt, "Source")
		search(t, tt, "backfill")
		mustText(t, tt, fmt.Sprint("1 of 2"))
	})
}
