package desktop_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	bigLog   = "logs/big.log"
	bigLines = 100000
	liveLog  = "logs/live.log"
)

func findFixture(t *testing.T) *fixture {
	t.Helper()
	f := docFixture(t)
	f.docWindowSize = 500
	lines := make([]string, bigLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %06d: nothing to see", i+1)
	}
	for i := 41; i < 3000; i += 100 {
		lines[i] = fmt.Sprintf("row %06d: ERROR out of memory", i+1)
	}
	lines[76] = "row 000077: disk quota at 90%"
	lines[59999] = "row 060000: zebra crossing"
	f.docLines[bigLog] = lines
	f.docs[bigLog] = desktop.Doc{Path: bigLog, Name: "big.log", Kind: "text", Text: true, Size: 3 << 20, At: at("1"), FolderPage: 1}
	live := make([]string, 800)
	for i := range live {
		live[i] = fmt.Sprintf("tick %04d", i+1)
	}
	f.docLines[liveLog] = live
	f.docs[liveLog] = desktop.Doc{Path: liveLog, Name: "live.log", Kind: "text", Text: true, Size: 9000, At: at("1"), FolderPage: 1}
	return f
}

func (f *fixture) grow(path string, lines ...string) {
	f.mu.Lock()
	f.docLines[path] = append(f.docLines[path], lines...)
	f.mu.Unlock()
}

func (f *fixture) served() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.linesServed
}

func brief(tt *ui.Tester) []string {
	var out []string
	for _, s := range tt.Texts() {
		if len(s) < 70 {
			out = append(out, s)
		}
	}
	return out
}

func openFind(t *testing.T, f *fixture, path string, dark bool) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := newApp(t, f)
	app.OpenDoc(path)
	tt := sized(t, app, windowWidth, windowHeight, dark)
	return app, tt
}

func search(t *testing.T, tt *ui.Tester, q string) {
	t.Helper()
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	if q == "" {
		tt.Key(0, ui.KeyBackspace)
	} else {
		tt.Type(q)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	tt.Frame()
}

func findCount(tt *ui.Tester) string {
	re := regexp.MustCompile(`^\d+ of \d+\+?$|^not in this file$|^Searching…$|^Find stopped: .*$`)
	for _, s := range tt.Texts() {
		if re.MatchString(s) {
			return s
		}
	}
	return ""
}

func markLine(t *testing.T, tt *ui.Tester) string {
	t.Helper()
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Line ") {
			return s
		}
	}
	return ""
}

func TestFindShowsTheFirstHitAndACountThatSaysThereIsMore(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			_, tt := openFind(t, findFixture(t), bigLog, dark)
			search(t, tt, "error")
			if got := findCount(tt); got != "1 of 5+" {
				t.Fatalf("count %q in %q", got, brief(tt))
			}
			if got := markLine(t, tt); got != "Line 42" {
				t.Fatalf("marked %q", got)
			}
		})
	}
}

func TestFindNextAndPreviousStepOneHitAndWrap(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	search(t, tt, "zebra")
	if got := findCount(tt); got != "1 of 1+" {
		t.Fatalf("count %q", got)
	}
	click(t, tt, "Next match")
	if got := findCount(tt); got != "1 of 1" {
		t.Fatalf("a single hit wraps to itself, got %q", got)
	}
	search(t, tt, "ERROR out")
	click(t, tt, "Next match")
	if got, line := findCount(tt), markLine(t, tt); got != "2 of 5+" || line != "Line 142" {
		t.Fatalf("after Next: %q %q", got, line)
	}
	click(t, tt, "Previous match")
	if got, line := findCount(tt), markLine(t, tt); got != "1 of 5+" || line != "Line 42" {
		t.Fatalf("after Previous: %q %q", got, line)
	}
	click(t, tt, "Previous match")
	if got := findCount(tt); got != "1 of 5+" {
		t.Fatalf("Previous at the first hit of an unfinished search stays, got %q", got)
	}
}

func TestFindStepsPastTheLoadedHitsByReadingMore(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	search(t, tt, "error")
	for i := 0; i < 5; i++ {
		click(t, tt, "Next match")
	}
	got := findCount(tt)
	if !strings.HasPrefix(got, "6 of ") || markLine(t, tt) != "Line 542" {
		t.Fatalf("the sixth hit: %q %q", got, markLine(t, tt))
	}
	if !strings.Contains(tt.Texts()[0]+strings.Join(tt.Texts(), "|"), "From line 522") {
		t.Fatalf("the window did not move to the hit: %q", brief(tt))
	}
}

func TestFindWrapsAtTheLastHitOnceTheScanIsDone(t *testing.T) {
	f := findFixture(t)
	f.docLines[bigLog][bigLines-1] = "row 100000: ERROR the last"
	_, tt := openFind(t, f, bigLog, false)
	search(t, tt, "zebra")
	click(t, tt, "Previous match")
	if got := findCount(tt); got != "1 of 1+" {
		t.Fatalf("got %q", got)
	}
	search(t, tt, "the last")
	if got := findCount(tt); got != "1 of 1" {
		t.Fatalf("got %q", got)
	}
	if markLine(t, tt) != "Line 100000" {
		t.Fatalf("marked %q", markLine(t, tt))
	}
}

func TestFindReadsOnlyTheRangesItNeeds(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	search(t, tt, "error")
	click(t, tt, "Next match")
	if n := f.served(); n >= bigLines/10 {
		t.Fatalf("find read %d of %d lines", n, bigLines)
	}
	if f.count("raw:"+bigLog) != 0 {
		t.Fatal("find fetched the whole file")
	}
}

func TestFindForAWordNotInTheFileSaysSoAfterReadingIt(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	search(t, tt, "quokka")
	if got := findCount(tt); got != "not in this file" {
		t.Fatalf("got %q", got)
	}
	if f.served() < bigLines {
		t.Fatalf("an absent word needs the whole file, read %d", f.served())
	}
}

func TestFindIsCaseInsensitiveAndTrimsTheQuery(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	search(t, tt, "  DISK QUOTA ")
	if got := findCount(tt); got != "1 of 1+" || markLine(t, tt) != "Line 77" {
		t.Fatalf("got %q %q", got, markLine(t, tt))
	}
}

func TestFindEnterStepsAndShiftEnterGoesBack(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	search(t, tt, "error")
	tt.Key(0, ui.KeyEnter)
	if got := markLine(t, tt); got != "Line 142" {
		t.Fatalf("Enter: %q", got)
	}
	tt.Key(ui.Shift, ui.KeyEnter)
	if got := markLine(t, tt); got != "Line 42" {
		t.Fatalf("Shift+Enter: %q", got)
	}
}

func TestFindEscapeClearsTheQueryAndTheHits(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	search(t, tt, "error")
	tt.Key(0, ui.KeyEscape)
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if got := findCount(tt); got != "" {
		t.Fatalf("count %q after Escape", got)
	}
	if !tt.HasText("Back") {
		t.Fatal("the page closed on the first Escape")
	}
}

func TestSlashAlsoFocusesFindAndTypingASlashInTheBoxDoesNotRefocus(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	tt.Key(0, ui.KeySlash)
	tt.Type("a/b")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if got := findCount(tt); got != "not in this file" {
		t.Fatalf("got %q", got)
	}
}

type stallWindow struct {
	desktop.ReadModel
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	held    bool
	armed   bool
}

func (s *stallWindow) DocWindow(ctx context.Context, fleet, path string, from int) (desktop.DocWindow, error) {
	w, err := s.ReadModel.DocWindow(ctx, fleet, path, from)
	s.mu.Lock()
	hold := s.armed && path == bigLog && from == 1 && !s.held
	s.held = s.held || hold
	s.mu.Unlock()
	if hold {
		s.entered <- struct{}{}
		<-s.release
	}
	return w, err
}

func TestALateFindAnswerDoesNotOverwriteANewerOne(t *testing.T) {
	model := &stallWindow{ReadModel: findFixture(t), entered: make(chan struct{}, 1), release: make(chan struct{})}
	app := newDesktop(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.OpenDoc(bigLog)
	drain(posted)
	tt := newTester(app)
	tt.Frame()
	model.mu.Lock()
	model.armed = true
	model.mu.Unlock()
	search(t, tt, "zebra")
	select {
	case <-model.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first scan never started")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("disk")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	drain(posted)
	tt.Frame()
	close(model.release)
	drain(posted)
	tt.Frame()
	if got, line := findCount(tt), markLine(t, tt); got != "1 of 1+" || line != "Line 77" {
		t.Fatalf("the late answer won: %q %q in %q", got, line, brief(tt))
	}
}

type errWindow struct {
	desktop.ReadModel
}

func (e errWindow) DocWindow(ctx context.Context, fleet, path string, from int) (desktop.DocWindow, error) {
	if path == bigLog && from > 1 {
		return desktop.DocWindow{}, errors.New("the connection was reset")
	}
	return e.ReadModel.DocWindow(ctx, fleet, path, from)
}

func TestAFindThatFailsSaysSoInsteadOfNoMatch(t *testing.T) {
	model := errWindow{findFixture(t)}
	app := newDesktop(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.OpenDoc(bigLog)
	tt := sized(t, app, windowWidth, windowHeight, false)
	search(t, tt, "zebra")
	if got := findCount(tt); !strings.HasPrefix(got, "Find stopped: ") || strings.Contains(got, noMatchWord) {
		t.Fatalf("got %q", got)
	}
}

const noMatchWord = "not in this file"

func TestGoToLineInTheWindowMarksIt(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	tt.Key(ui.Cmd, ui.KeyF)
	click(t, tt, "Go to line")
	tt.Type("250")
	click(t, tt, "Go")
	if got := markLine(t, tt); got != "Line 250" {
		t.Fatalf("marked %q", got)
	}
}

func TestGoToLineOutsideTheWindowLoadsItAndMarksThatLine(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	click(t, tt, "Go to line")
	tt.Type("60000")
	tt.Key(0, ui.KeyEnter)
	if got := markLine(t, tt); got != "Line 60000" {
		t.Fatalf("marked %q in %q", got, brief(tt))
	}
	mustText(t, tt, "From line 59980")
	if f.served() > 2000 {
		t.Fatalf("a jump read %d lines", f.served())
	}
}

func TestGoToLineOnTheFirstAndLastLine(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	for _, n := range []string{"1", "100000"} {
		click(t, tt, "Go to line")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(n)
		click(t, tt, "Go")
		if got := markLine(t, tt); got != "Line "+n {
			t.Fatalf("line %s marked %q", n, got)
		}
	}
}

func TestGoToLinePastTheEndSaysSo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"100001", "This file has no line 100,001; it ends at line 100,000."},
		{"300000", "This file has no line 300,000."},
		{"0", "Enter a line number, 1 or more."},
		{"-4", "Enter a line number, 1 or more."},
		{"abc", "Enter a line number, 1 or more."},
		{"", "Enter a line number, 1 or more."},
	} {
		t.Run(tc.in, func(t *testing.T) {
			_, tt := openFind(t, findFixture(t), bigLog, true)
			click(t, tt, "Go to line")
			tt.Type(tc.in)
			click(t, tt, "Go")
			mustText(t, tt, tc.want)
			if got := markLine(t, tt); got != "" {
				t.Fatalf("marked %q", got)
			}
		})
	}
}

func TestFindAndGoToLineInMarkdownSource(t *testing.T) {
	f := findFixture(t)
	app, tt := openFind(t, f, "reports/t1/design.md", false)
	if _, ok := tt.Find("Find in file"); ok {
		t.Fatal("Rendered shows a find box")
	}
	click(t, tt, "Source")
	search(t, tt, "backfill")
	if got := findCount(tt); got != "1 of 2" {
		t.Fatalf("got %q in %q", got, brief(tt))
	}
	click(t, tt, "Next match")
	if got := findCount(tt); got != "2 of 2" {
		t.Fatalf("got %q", got)
	}
	click(t, tt, "Go to line")
	tt.Type("9999")
	click(t, tt, "Go")
	mustText(t, tt, "This file has no line 9,999; it ends at line 25.")
	if f.count("window:reports/t1/design.md:1") != 0 {
		t.Fatal("source find read ranges of a file it already holds")
	}
	if _, ok := tt.Find("Follow"); ok {
		t.Fatal("Markdown source offers Follow")
	}
	_ = app
}

func TestFollowTailsAGrowingFileAndStopsWhenThePersonScrollsUp(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			f := findFixture(t)
			_, tt := openFind(t, f, liveLog, dark)
			click(t, tt, "Follow")
			mustText(t, tt, "Following", "tick 0800")
			f.grow(liveLog, "tick 0801", "tick 0802")
			tt.Advance(2500 * time.Millisecond)
			tt.Frame()
			mustText(t, tt, "tick 0802", "Following")
			if f.count("window:"+liveLog+":800") == 0 {
				t.Fatalf("no poll from the last line: %v", f.calls)
			}
			tt.Scroll(500, 400, 0, -3000)
			tt.Frame()
			mustNotText(t, tt, "Following")
			mustText(t, tt, "Follow stopped: you scrolled up.")
			f.grow(liveLog, "tick 0803")
			polls := f.count("window:"+liveLog+":800") + f.count("window:"+liveLog+":802")
			tt.Advance(10 * time.Second)
			tt.Frame()
			mustNotText(t, tt, "tick 0803")
			if f.count("window:"+liveLog+":800")+f.count("window:"+liveLog+":802") != polls || f.count("window:"+liveLog+":803") != 0 {
				t.Fatalf("polling went on after the stop: %v", f.calls)
			}
		})
	}
}

func TestFollowStaysAtTheEndAsLinesArrive(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	for i := 0; i < 3; i++ {
		f.grow(liveLog, fmt.Sprintf("tick %04d", 801+i))
		tt.Advance(2500 * time.Millisecond)
		tt.Frame()
		tt.Frame()
		mustText(t, tt, fmt.Sprintf("tick %04d", 801+i), "Following")
	}
}

func TestFollowButtonTogglesOff(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	click(t, tt, "Following")
	mustNotText(t, tt, "Following")
	f.grow(liveLog, "tick 0801")
	tt.Advance(10 * time.Second)
	tt.Frame()
	mustNotText(t, tt, "tick 0801")
}

func TestFollowSaysWhenTheFileWasReplaced(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	f.mu.Lock()
	f.docLines[liveLog] = []string{"fresh start"}
	f.mu.Unlock()
	tt.Advance(2500 * time.Millisecond)
	tt.Frame()
	mustText(t, tt, "The file was replaced or shortened; reload to read it again.")
	mustNotText(t, tt, "Following")
}

func TestFollowSaysWhenTheFileChangedUnderTheLastLine(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	f.mu.Lock()
	f.docLines[liveLog][799] = "something else entirely"
	f.mu.Unlock()
	tt.Advance(2500 * time.Millisecond)
	tt.Frame()
	mustText(t, tt, "The file was replaced or shortened; reload to read it again.")
}

func TestEndJumpsToTheLastWindowWithoutReadingTheFile(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	click(t, tt, "End")
	mustText(t, tt, "row 100000: nothing to see", "row 099999: nothing to see")
	if n := f.served(); n > 10000 {
		t.Fatalf("End read %d lines", n)
	}
}

func TestEndOnAShortFileAndAnEmptyOne(t *testing.T) {
	f := findFixture(t)
	f.docLines["reports/t1/empty.txt"] = nil
	f.docs["reports/t1/empty.txt"] = desktop.Doc{Path: "reports/t1/empty.txt", Name: "empty.txt", Kind: "text", Text: true, FolderPage: 1}
	app := newApp(t, f)
	app.OpenDoc("reports/t1/empty.txt")
	tt := newTester(app)
	click(t, tt, "End")
	mustText(t, tt, "This file is empty.")
	click(t, tt, "Follow")
	f.grow("reports/t1/empty.txt", "first line")
	tt.Advance(2500 * time.Millisecond)
	tt.Frame()
	mustText(t, tt, "first line")
}

func TestFindTabOrderFollowsTheToolbar(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	search(t, tt, "error")
	order := []string{"Previous match", "Next match", "Go to line", "Go", "Follow", "End"}
	for _, want := range order {
		tt.Key(0, ui.KeyTab)
		if !tt.Focused(want) {
			t.Fatalf("Tab did not reach %q", want)
		}
	}
}

func TestGoToLineJustPastTheWindowReadsTheNextOne(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	for _, n := range []string{"500", "501"} {
		click(t, tt, "Go to line")
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(n)
		click(t, tt, "Go")
		if got := markLine(t, tt); got != "Line "+n {
			t.Fatalf("line %s: marked %q in %q", n, got, brief(tt))
		}
	}
	mustText(t, tt, "From line 481")
}

func inView(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, brief(tt))
	}
	if r.Y < 140 || r.Y+r.H > windowHeight {
		t.Fatalf("%q at %v is outside the page shown", label, r)
	}
}

func TestGoToLineAndNextMatchInsideTheWindowScrollTheTargetIntoView(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			_, tt := openFind(t, findFixture(t), bigLog, dark)
			click(t, tt, "Go to line")
			tt.Type("300")
			click(t, tt, "Go")
			tt.Frame()
			inView(t, tt, "Line 300")
			search(t, tt, "error")
			click(t, tt, "Next match")
			tt.Frame()
			inView(t, tt, "Line 142")
			click(t, tt, "Next match")
			tt.Frame()
			inView(t, tt, "Line 242")
			click(t, tt, "Previous match")
			tt.Frame()
			inView(t, tt, "Line 142")
		})
	}
}

func TestAMarkedLineIsNotScrolledBackToAfterThePersonScrolls(t *testing.T) {
	_, tt := openFind(t, findFixture(t), bigLog, false)
	click(t, tt, "Go to line")
	tt.Type("300")
	click(t, tt, "Go")
	tt.Frame()
	inView(t, tt, "Line 300")
	tt.Scroll(500, 400, 0, -6000)
	tt.Frame()
	tt.Frame()
	if r, ok := tt.Find("Line 300"); ok && r.Y >= 140 && r.Y+r.H <= windowHeight {
		t.Fatalf("the page was pulled back to the marked line: %v", r)
	}
}

func TestGoToLineStopsFollowing(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	mustText(t, tt, "Following")
	click(t, tt, "Go to line")
	tt.Type("10")
	click(t, tt, "Go")
	mustNotText(t, tt, "Following")
	mustText(t, tt, "Line 10")
	polls := f.count("window:" + liveLog + ":800")
	f.grow(liveLog, "tick 0801")
	tt.Advance(10 * time.Second)
	tt.Frame()
	if f.count("window:"+liveLog+":800") != polls {
		t.Fatalf("polling went on after Go to line: %v", f.calls)
	}
}

func TestPreviousWrapsFromTheFirstHitOnceTheSearchIsDone(t *testing.T) {
	_, tt := openFind(t, findFixture(t), "reports/t1/design.md", false)
	click(t, tt, "Source")
	search(t, tt, "backfill")
	click(t, tt, "Previous match")
	if got := findCount(tt); got != "2 of 2" {
		t.Fatalf("Previous from the first hit: %q", got)
	}
}

func TestFindKeepsAtMostTwoHundredHits(t *testing.T) {
	_, tt := openFind(t, findFixture(t), liveLog, false)
	search(t, tt, "tick")
	if got := findCount(tt); got != "1 of 200" {
		t.Fatalf("got %q", got)
	}
}

func TestFindWaitsForTheTypingToPauseBeforeReading(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, bigLog, false)
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	before := f.served()
	tt.Type("error")
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if f.served() != before || findCount(tt) != "" {
		t.Fatalf("read after 100 ms of typing: %q", findCount(tt))
	}
	tt.Advance(200 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	if findCount(tt) != "1 of 5+" {
		t.Fatalf("after the pause: %q", findCount(tt))
	}
}

func TestFollowKeepsTheLastFiveThousandLines(t *testing.T) {
	f := findFixture(t)
	_, tt := openFind(t, f, liveLog, false)
	click(t, tt, "Follow")
	more := make([]string, 5400)
	for i := range more {
		more[i] = fmt.Sprintf("tick %04d", 801+i)
	}
	f.grow(liveLog, more...)
	for i := 0; i < 20; i++ {
		tt.Advance(2500 * time.Millisecond)
		tt.Frame()
	}
	mustText(t, tt, "From line 1201", "tick 6200")
}

func TestEndShowsTheLastHundredLinesWhenTheTailWindowIsShort(t *testing.T) {
	f := findFixture(t)
	lines := make([]string, 1001)
	for i := range lines {
		lines[i] = fmt.Sprintf("entry %04d", i+1)
	}
	f.docLines["logs/tail.log"] = lines
	f.docs["logs/tail.log"] = desktop.Doc{Path: "logs/tail.log", Name: "tail.log", Kind: "text", Text: true, Size: 12000, At: at("1"), FolderPage: 1}
	_, tt := openFind(t, f, "logs/tail.log", false)
	click(t, tt, "End")
	mustText(t, tt, "From line 902", "entry 0902", "entry 1001")
	mustNotText(t, tt, "entry 0901")
}

type holdWindow struct {
	desktop.ReadModel
	mu      sync.Mutex
	hold    func(path string, from int) bool
	entered chan struct{}
	release chan struct{}
	held    bool
	empty   func(path string, from int) bool
}

func (h *holdWindow) DocWindow(ctx context.Context, fleet, path string, from int) (desktop.DocWindow, error) {
	w, err := h.ReadModel.DocWindow(ctx, fleet, path, from)
	h.mu.Lock()
	hold := h.hold != nil && h.hold(path, from) && !h.held
	h.held = h.held || hold
	empty := h.empty != nil && h.empty(path, from)
	h.mu.Unlock()
	if hold {
		h.entered <- struct{}{}
		<-h.release
	}
	if empty {
		return desktop.DocWindow{Path: w.Path, Name: w.Name, From: from}, nil
	}
	return w, err
}

func (h *holdWindow) arm(hold func(path string, from int) bool) {
	h.mu.Lock()
	h.hold, h.held = hold, false
	h.mu.Unlock()
}

func asyncHold(t *testing.T) (*holdWindow, chan func(), *ui.Tester) {
	t.Helper()
	model := &holdWindow{ReadModel: findFixture(t), entered: make(chan struct{}, 4), release: make(chan struct{})}
	app := newDesktop(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	posted := make(chan func(), 32)
	app.Post = func(fn func()) { posted <- fn }
	app.OpenDoc(bigLog)
	drain(posted)
	tt := newTester(app)
	tt.Frame()
	return model, posted, tt
}

func waitHeld(t *testing.T, model *holdWindow) {
	t.Helper()
	select {
	case <-model.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the held read never started")
	}
}

func TestGoToLineInTheWindowCancelsAPendingFollowSeek(t *testing.T) {
	model, posted, tt := asyncHold(t)
	model.arm(func(path string, from int) bool { return path == bigLog && from > 1 })
	click(t, tt, "Follow")
	waitHeld(t, model)
	click(t, tt, "Go to line")
	tt.Type("5")
	click(t, tt, "Go")
	close(model.release)
	drain(posted)
	tt.Frame()
	tt.Frame()
	mustNotText(t, tt, "row 100000: nothing to see")
	if line := markLine(t, tt); line != "Line 5" {
		t.Fatalf("the seek took over: %q in %q", line, brief(tt))
	}
}

func TestEndWhileFollowingIgnoresAPullInFlight(t *testing.T) {
	model, posted, tt := asyncHold(t)
	click(t, tt, "Follow")
	drain(posted)
	tt.Frame()
	mustText(t, tt, "Following", "row 100000: nothing to see")
	model.arm(func(path string, from int) bool { return path == bigLog && from == bigLines })
	tt.Advance(2500 * time.Millisecond)
	tt.Frame()
	waitHeld(t, model)
	model.ReadModel.(*fixture).grow(bigLog, "late one", "late two")
	click(t, tt, "End")
	drain(posted)
	tt.Frame()
	close(model.release)
	drain(posted)
	tt.Frame()
	tt.Frame()
	mustText(t, tt, "late one", "late two", "Following")
	mustNotText(t, tt, "The file was replaced or shortened; reload to read it again.")
}

func TestFollowStopsWhenTheFileComesBackEmpty(t *testing.T) {
	f := findFixture(t)
	model := &holdWindow{ReadModel: f, entered: make(chan struct{}, 1), release: make(chan struct{})}
	app := newDesktop(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.OpenDoc(liveLog)
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "Follow")
	mustText(t, tt, "Following")
	model.mu.Lock()
	model.empty = func(path string, from int) bool { return path == liveLog && from >= 800 }
	model.mu.Unlock()
	tt.Advance(2500 * time.Millisecond)
	tt.Frame()
	tt.Frame()
	mustText(t, tt, "The file was replaced or shortened; reload to read it again.")
	mustNotText(t, tt, "Following")
}
