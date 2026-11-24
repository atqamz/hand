package desktop_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func older(t *testing.T, tt *ui.Tester) {
	t.Helper()
	tt.Frame()
	tt.Scroll(600, 400, 0, 4000)
	tt.Frame()
	click(t, tt, "Older messages")
}

func searchFor(t *testing.T, tt *ui.Tester, q string) {
	t.Helper()
	click(t, tt, "Search the conversation")
	tt.Type(q)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
}

func TestTheConversationPageReadsTheTimelineAndPagesOlderByTheBoardsCursor(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			app := historyApp(t, m)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "Fleets / golden / conversation", "Conversation", "Build is green. Shipping the amber fix.", "ship it", "Older messages")
			mustNotText(t, tt, "what about the kiln schedule?")
			older(t, tt)
			tt.Frame()
			mustText(t, tt, "what about the kiln schedule?", "Older messages")
			mustNotText(t, tt, "start the work")
			older(t, tt)
			tt.Frame()
			mustText(t, tt, "start the work", "ship it")
			mustNotText(t, tt, "Older messages")
			if got, want := m.olders(), []int{40, 20}; !slices.Equal(got, want) {
				t.Fatalf("timeline reads before = %v, want %v", got, want)
			}
		})
	}
}

func TestSearchShowsHitsWithTheMatchedWordsAndTheCount(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			app := historyApp(t, m)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			searchFor(t, tt, "kiln")
			mustText(t, tt, "Showing 3 of 4", "You", "Supervisor", "Report", "i7", "y2", "r5", "More matches")
			mustText(t, tt, "what about the kiln schedule?", "…the kiln build passed…")
			mustNotText(t, tt, "Build is green. Shipping the amber fix.")
			if len(m.searches) != 1 || m.searches[0] != (histCall{"kiln", 20}) {
				t.Fatalf("searches = %v, want one for kiln at limit 20", m.searches)
			}
			click(t, tt, "More matches")
			tt.Frame()
			if len(m.searches) != 2 || m.searches[1] != (histCall{"kiln", 50}) {
				t.Fatalf("searches = %v, want a second at limit 50", m.searches)
			}
		})
	}
}

func TestAnEmptySearchClearsTheHitsAndShowsTheTimeline(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	mustText(t, tt, "i7")
	click(t, tt, "Search the conversation")
	for range 4 {
		tt.Key(0, ui.KeyBackspace)
	}
	click(t, tt, "Search")
	tt.Frame()
	if _, ok := tt.Find("Open i7 from You"); ok {
		t.Fatal("the hits stayed after a search with no words")
	}
	mustText(t, tt, "Build is green. Shipping the amber fix.")
}

func TestClearSearchReturnsToTheTimeline(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	click(t, tt, "Clear search")
	tt.Frame()
	mustNotText(t, tt, "Showing 3 of 4")
	mustText(t, tt, "Build is green. Shipping the amber fix.", "Search what was said: your messages, the supervisor's replies, reports, decisions and task titles.")
}

func TestASearchWithNoMatchesSaysSo(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "ghost")
	mustText(t, tt, "No matches", "Nothing said here matches “ghost”.")
}

func TestALateSearchAnswerDoesNotOverwriteANewerOne(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			m.gate = map[string]chan struct{}{"kiln": make(chan struct{})}
			m.entered = make(chan string, 2)
			app := historyApp(t, m)
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			tt := sized(t, app, windowWidth, windowHeight, dark)
			click(t, tt, "Search the conversation")
			tt.Type("kiln")
			tt.Key(0, ui.KeyEnter)
			select {
			case <-m.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("the slow search never started")
			}
			click(t, tt, "Search the conversation")
			for range 4 {
				tt.Key(0, ui.KeyBackspace)
			}
			tt.Type("amber")
			tt.Key(0, ui.KeyEnter)
			waitPost(t, posted)()
			tt.Frame()
			mustText(t, tt, "1 match", "y9")
			close(m.gate["kiln"])
			waitPost(t, posted)()
			tt.Frame()
			mustText(t, tt, "1 match", "y9")
			mustNotText(t, tt, "i7")
		})
	}
}

func TestOpeningAHitShowsTheWholeEntryAndShowInConversationFocusesIt(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			app := historyApp(t, m)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			searchFor(t, tt, "kiln")
			click(t, tt, "Open i7 from You")
			tt.Frame()
			mustText(t, tt, "what about the kiln schedule?", "Show in conversation", "Back to results")
			click(t, tt, "Show in conversation")
			tt.Frame()
			if got, want := m.olders(), []int{40}; !slices.Equal(got, want) {
				t.Fatalf("timeline reads before = %v, want %v: the entry sits on the first older page", got, want)
			}
			r, ok := tt.Find("Matched entry i7")
			if !ok {
				t.Fatalf("no focused entry in %q", tt.Texts())
			}
			if _, ok := tt.Find("Matched entry i12"); ok {
				t.Fatal("the newest operator message was focused too")
			}
			text, ok := tt.Find("what about the kiln schedule?")
			if !ok || text.Y < r.Y-1 {
				t.Fatalf("the matched text is not inside the focused entry: %v %v", text, r)
			}
			mustNotText(t, tt, "start the work")
		})
	}
}

func TestMatchedEntryBoxMarksExactlyOneEntry(t *testing.T) {
	m := historyModel(t)
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	click(t, tt, "Open i7 from You")
	tt.Frame()
	click(t, tt, "Show in conversation")
	tt.Frame()
	n := 0
	for _, ref := range []string{"i3", "i7", "i12", "y1", "y2", "y9", "r5"} {
		if _, ok := tt.Find("Matched entry " + ref); ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d entries carry the Matched entry box, want 1", n)
	}
	if _, ok := tt.Find("Matched entry i7"); !ok {
		t.Fatal("the box is not on i7")
	}
}

func TestAFailingOlderReadShowsItsErrorLine(t *testing.T) {
	m := historyModel(t)
	m.olderErr = errors.New("the board did not answer in time")
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	older(t, tt)
	tt.Frame()
	mustText(t, tt, "Older messages could not be read: the board did not answer in time", "Older messages")
	mustNotText(t, tt, "what about the kiln schedule?")
}

func TestEachDayOfTheConversationGetsOneDivider(t *testing.T) {
	m := historyModel(t)
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "Tue, Nov 24")
	mustNotText(t, tt, "Mon, Nov 23")
	older(t, tt)
	tt.Frame()
	older(t, tt)
	tt.Frame()
	mustText(t, tt, "Tue, Nov 24", "Mon, Nov 23")
	days := 0
	for _, s := range tt.Texts() {
		if s == "Tue, Nov 24" {
			days++
		}
	}
	if days != 1 {
		t.Fatalf("the day Tue, Nov 24 has %d dividers over its five entries, want 1", days)
	}
}

func TestAFullPageOfHitsSaysToNarrowTheSearch(t *testing.T) {
	m := historyModel(t)
	var hits []desktop.HistoryHit
	for i := range 50 {
		hits = append(hits, desktop.HistoryHit{Ref: "i" + strconv.Itoa(100+i), Who: "operator", At: "2026-11-24T09:00:00Z", Text: "a «many» message"})
	}
	m.hits["many"] = desktop.HistorySearch{Q: "many", Total: 80, More: true, Hits: hits}
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "many")
	app.MoreHistory()
	tt.Frame()
	mustText(t, tt, "Showing the first 50 of 80. Narrow the search.")
	mustNotText(t, tt, "More matches")
}

func TestShowInConversationPicksTheReplyThatSharesATimestampByItsText(t *testing.T) {
	m := historyModel(t)
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	click(t, tt, "Open y2 from Supervisor")
	tt.Frame()
	click(t, tt, "Show in conversation")
	tt.Frame()
	r, ok := tt.Find("Matched entry y2")
	if !ok {
		t.Fatalf("no focused reply in %q", tt.Texts())
	}
	second, _ := tt.Find("The second reply at nine, about the kiln.")
	first, _ := tt.Find("The first reply at nine.")
	if second.Y < r.Y-1 || first.Y >= r.Y-1 && first.Y <= r.Y+r.H {
		t.Fatalf("focus is on the wrong reply: focus %v first %v second %v", r, first, second)
	}
}

func TestAnEntryNoLongerInTheConversationSaysSo(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "gone")
	click(t, tt, "Open i99 from You")
	tt.Frame()
	click(t, tt, "Show in conversation")
	tt.Frame()
	mustText(t, tt, "That entry is no longer in the conversation view.")
	if _, ok := tt.Find("Matched entry y2"); ok {
		t.Fatal("an entry was focused that is not there")
	}
}

func TestAReportHitOpensItsRefAndNotTheConversation(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	click(t, tt, "Open r5 from Report")
	tt.Frame()
	mustText(t, tt, "The kiln build passed.", "Task t4", "Cut at 16 KiB.", "Open r5")
	mustNotText(t, tt, "Show in conversation")
}

func TestTheConversationPageShowsItsErrorsWithRetry(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			m.timelineErr = errors.New("the board did not answer in time")
			app := historyApp(t, m)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "The conversation could not be read", "the board did not answer in time", "Retry")
			mustNotText(t, tt, "Build is green. Shipping the amber fix.")
			m.mu.Lock()
			m.timelineErr = nil
			m.mu.Unlock()
			click(t, tt, "Retry")
			tt.Frame()
			mustText(t, tt, "Build is green. Shipping the amber fix.")
			mustNotText(t, tt, "The conversation could not be read")
		})
	}
}

func TestASearchErrorIsShownAndRetryReadsAgain(t *testing.T) {
	m := historyModel(t)
	m.searchErr = errors.New("the board did not answer in time")
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	mustText(t, tt, "The conversation could not be searched", "the board did not answer in time", "Retry")
	mustNotText(t, tt, "i7")
	m.mu.Lock()
	m.searchErr = nil
	m.mu.Unlock()
	click(t, tt, "Retry")
	tt.Frame()
	mustText(t, tt, "i7")
	mustNotText(t, tt, "The conversation could not be searched")
}

func TestAnEntryReadErrorIsShownWithRetry(t *testing.T) {
	m := historyModel(t)
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	m.mu.Lock()
	m.entryErr = errors.New("the board did not answer in time")
	m.mu.Unlock()
	click(t, tt, "Open i7 from You")
	tt.Frame()
	mustText(t, tt, "The entry could not be read", "the board did not answer in time")
	m.mu.Lock()
	m.entryErr = nil
	m.mu.Unlock()
	click(t, tt, "Retry")
	tt.Frame()
	mustText(t, tt, "Show in conversation")
}

func TestAnOlderHostGetsOneSentenceAndStillReadsTheConversation(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := historyModel(t)
			m.features = []string{"fleets"}
			app := historyApp(t, m)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			mustText(t, tt, "This host is too old to search the conversation", "Build is green. Shipping the amber fix.")
			mustNotText(t, tt, "Search")
		})
	}
}

func tabUntil(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for range 40 {
		if tt.Focused(label) {
			return
		}
		tt.Key(0, ui.KeyTab)
	}
	t.Fatalf("tab never reached %q", label)
}

func TestTabWalksTheSearchFieldTheButtonThenTheTimelineAndTheHits(t *testing.T) {
	app := historyApp(t, historyModel(t))
	tt := sized(t, app, windowWidth, windowHeight, false)
	tabUntil(t, tt, "Search the conversation")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Search the conversation") || tt.Focused("Search") {
		t.Fatal("tab did not reach the search field")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Search") || tt.Focused("Search the conversation") {
		t.Fatal("tab did not leave the field for the Search button")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Older messages") {
		t.Fatal("tab did not reach Older messages")
	}
	searchFor(t, tt, "kiln")
	for _, want := range []string{"Search", "Clear search"} {
		tt.Key(0, ui.KeyTab)
		if !tt.Focused(want) {
			t.Fatalf("tab did not reach %q", want)
		}
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Open i7 from You") {
		t.Fatalf("tab did not reach the first hit")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Open y2 from Supervisor") {
		t.Fatalf("tab did not reach the second hit")
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	mustText(t, tt, "Show in conversation")
}

func TestTheRailsConversationItemOpensThePage(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Conversation")
	tt.Frame()
	if app.Screen != desktop.HistoryScreen {
		t.Fatalf("screen = %v, want the conversation page", app.Screen)
	}
}

func TestScenesTheConversationPage(t *testing.T) {
	cases := map[string]func(t *testing.T, m *histModel, tt *ui.Tester){
		"110-history-page": func(t *testing.T, m *histModel, tt *ui.Tester) {
			older(t, tt)
			tt.Frame()
		},
		"111-history-hits": func(t *testing.T, m *histModel, tt *ui.Tester) { searchFor(t, tt, "kiln") },
		"112-history-empty": func(t *testing.T, m *histModel, tt *ui.Tester) {
			searchFor(t, tt, "ghost")
		},
		"113-history-error": func(t *testing.T, m *histModel, tt *ui.Tester) {
			m.searchErr = errors.New("the board did not answer in time")
			searchFor(t, tt, "kiln")
		},
		"114-history-old": func(t *testing.T, m *histModel, tt *ui.Tester) {},
	}
	for base, step := range cases {
		for _, dark := range []bool{false, true} {
			name := base + "-1100x760-" + theme(dark)
			t.Run(name, func(t *testing.T) {
				m := historyModel(t)
				if base == "114-history-old" {
					m.features = []string{"fleets"}
				}
				app := historyApp(t, m)
				tt := sized(t, app, windowWidth, windowHeight, dark)
				step(t, m, tt)
				shotSized(t, tt, name, windowWidth, windowHeight)
			})
		}
	}
}

func TestANewSearchDropsTheOldHitsWhileItWaits(t *testing.T) {
	m := historyModel(t)
	m.gate = map[string]chan struct{}{"amber": make(chan struct{})}
	m.entered = make(chan string, 1)
	app := historyApp(t, m)
	tt := sized(t, app, windowWidth, windowHeight, false)
	searchFor(t, tt, "kiln")
	mustText(t, tt, "i7")
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	click(t, tt, "Search the conversation")
	for range 4 {
		tt.Key(0, ui.KeyBackspace)
	}
	tt.Type("amber")
	tt.Key(0, ui.KeyEnter)
	<-m.entered
	tt.Frame()
	mustNotText(t, tt, "i7")
	mustText(t, tt, "Searching…")
	close(m.gate["amber"])
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "y9")
}
