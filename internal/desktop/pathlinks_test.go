package desktop_test

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo/ui"
)

type pathCase struct {
	name       string
	dark       bool
	w, h       int
	sizeNarrow bool
}

var pathSizes = func() []pathCase {
	var out []pathCase
	for _, dark := range []bool{false, true} {
		for _, s := range []struct {
			name string
			w, h int
		}{{"wide", 1100, 760}, {"narrow", 480, 760}} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			out = append(out, pathCase{name: theme + "-" + s.name, dark: dark, w: s.w, h: s.h})
		}
	}
	return out
}()

func supervisor(text string) desktop.Entry {
	return desktop.Entry{Role: "supervisor", Ref: "s1", No: 9, At: "2026-11-20T12:06:00Z", Text: text}
}

func link(rel string) desktop.Link {
	return desktop.Link{Rel: rel, Href: "/" + fleetID + "/doc/" + rel}
}

func addDoc(f *fixture, rel string) {
	f.docs[rel] = desktop.Doc{Path: rel, Name: rel[strings.LastIndex(rel, "/")+1:], Kind: "markdown", Text: true, Size: 5, At: "2026-11-20T12:00:00Z", FolderPage: 1}
	f.docBytes[rel] = []byte("# " + rel)
}

func pathFixture(t *testing.T, links map[string]string, entries ...desktop.Entry) *fixture {
	t.Helper()
	inUTC(t)
	f := goldenFixture(t)
	f.fleets[0].Home = installFakeHand(t).home
	f.features = []string{"events", "links"}
	f.linkTable = map[string]desktop.Link{}
	f.docs, f.docBytes = map[string]desktop.Doc{}, map[string][]byte{}
	for p, rel := range links {
		f.linkTable[p] = link(rel)
		addDoc(f, rel)
	}
	tl := f.timeline[fleetID]
	tl.Entries = entries
	f.timeline[fleetID] = tl
	return f
}

func pathApp(t *testing.T, f *fixture, c pathCase) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := newApp(t, f)
	tt := sized(t, app, c.w, c.h, c.dark)
	if c.w < 900 {
		click(t, tt, "Chat")
	}
	return app, tt
}

func notALink(t *testing.T, tt *ui.Tester, app *desktop.App, text string) {
	t.Helper()
	if err := tt.Click(text); err == nil && app.Screen == desktop.DocScreen {
		t.Fatalf("%q opened a document", text)
	}
}

func frames(tt *ui.Tester) {
	for range 3 {
		tt.Frame()
	}
}

func TestACodeSpanPathBecomesALinkAfterTheAnswerAndOpensTheDoc(t *testing.T) {
	for _, c := range pathSizes {
		t.Run(c.name, func(t *testing.T) {
			f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"},
				supervisor("Read `reports/a.md` and `reports/missing.md` now."))
			app, tt := pathApp(t, f, c)
			frames(tt)
			mustText(t, tt, "reports/a.md", "reports/missing.md")
			if got := f.requests(); len(got) != 1 || strings.Join(got[0], " ") != "reports/a.md reports/missing.md" {
				t.Fatalf("requests = %q", got)
			}
			notALink(t, tt, app, "reports/missing.md")
			click(t, tt, "reports/a.md")
			if app.Screen != desktop.DocScreen || desktop.DocShown(app) != "reports/a.md" {
				t.Fatalf("screen %v, doc %q", app.Screen, desktop.DocShown(app))
			}
		})
	}
}

func TestAPathInProseLinksToo(t *testing.T) {
	f := pathFixture(t, map[string]string{"/very/long/fleet/reports/a.md": "reports/a.md"},
		supervisor("Wrote /very/long/fleet/reports/a.md, then stopped."))
	app, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	click(t, tt, "/very/long/fleet/reports/a.md")
	if desktop.DocShown(app) != "reports/a.md" {
		t.Fatalf("doc %q", desktop.DocShown(app))
	}
}

func TestPathsInFencedCodeLinksAndTheOperatorsTextNeverLink(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"},
		supervisor("```\nreports/a.md\n/abs/a.md\n```\n\n[reports/a.md](https://example.test/x)\n\n![alt](reports/a.md)"),
		operator("see `reports/a.md` and /abs/a.md"))
	app, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	if got := f.requests(); len(got) != 0 {
		t.Fatalf("requests = %q, want none", got)
	}
	if f.count("features") != 0 {
		t.Fatal("meta read although nothing is a candidate")
	}
	notALink(t, tt, app, "reports/a.md")
	notALink(t, tt, app, "/abs/a.md")
}

func TestOnlyTheFirstFiftyCandidatesOfAMessageAreSent(t *testing.T) {
	var b strings.Builder
	links := map[string]string{}
	for i := range 60 {
		p := fmt.Sprintf("reports/%02d.md", i)
		fmt.Fprintf(&b, "`%s` ", p)
		links[p] = p
	}
	f := pathFixture(t, links, supervisor(b.String()))
	_, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	got := f.requests()
	if len(got) != 1 || len(got[0]) != 50 || got[0][0] != "reports/00.md" || got[0][49] != "reports/49.md" {
		t.Fatalf("requests = %q", got)
	}
	mustText(t, tt, "reports/49.md")
}

func TestTheSamePathInTwoMessagesIsSentOnce(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"},
		supervisor("first `reports/a.md`"), supervisor("second `reports/a.md` and `reports/a.md`"))
	_, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	got := f.requests()
	if len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("requests = %q", got)
	}
}

func TestANegativeIsAskedAgainAfterThirtyOneSeconds(t *testing.T) {
	f := pathFixture(t, nil, supervisor("later `reports/a.md`"))
	app := newApp(t, f)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	app.Now = func() time.Time { return now }
	tt := sized(t, app, 1100, 760, false)
	frames(tt)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requests", n)
	}
	addDoc(f, "reports/a.md")
	f.mu.Lock()
	f.linkTable["reports/a.md"] = link("reports/a.md")
	f.mu.Unlock()
	now = now.Add(29 * time.Second)
	tt.Advance(29 * time.Second)
	frames(tt)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requests inside the negative's lifetime", n)
	}
	now = now.Add(2 * time.Second)
	tt.Advance(2 * time.Second)
	frames(tt)
	if n := len(f.requests()); n != 2 {
		t.Fatalf("%d requests after thirty-one seconds, want 2", n)
	}
	click(t, tt, "reports/a.md")
	if desktop.DocShown(app) != "reports/a.md" {
		t.Fatalf("doc %q", desktop.DocShown(app))
	}
}

func TestABoardWithoutTheLinksFeatureGetsNoLinksRequest(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"}, supervisor("`reports/a.md`"))
	f.features = []string{"events", "fleets"}
	_, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	if f.count("links") != 0 || f.count("features") != 1 {
		t.Fatalf("links %d, features %d", f.count("links"), f.count("features"))
	}
}

func TestABoardThatGainsTheLinksFeatureIsAskedAgainAfterThirtyOneSeconds(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"}, supervisor("`reports/a.md`"))
	f.features = []string{"events"}
	app := newApp(t, f)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	app.Now = func() time.Time { return now }
	tt := sized(t, app, 1100, 760, false)
	frames(tt)
	f.features = []string{"events", "links"}
	now = now.Add(31 * time.Second)
	tt.Advance(31 * time.Second)
	frames(tt)
	if f.count("features") != 2 || f.count("links") != 1 {
		t.Fatalf("features %d, links %d", f.count("features"), f.count("links"))
	}
	click(t, tt, "reports/a.md")
	if desktop.DocShown(app) != "reports/a.md" {
		t.Fatalf("doc %q", desktop.DocShown(app))
	}
}

func TestAModelErrorLeavesTheTextPlainWithoutARetryStorm(t *testing.T) {
	want := map[string]int{"links": 1, "features": 0}
	for name, set := range map[string]func(*fixture){
		"links":    func(f *fixture) { f.linksErr = fmt.Errorf("boom") },
		"features": func(f *fixture) { f.featuresErr = fmt.Errorf("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"}, supervisor("`reports/a.md`"))
			set(f)
			app, tt := pathApp(t, f, pathSizes[0])
			for range 20 {
				tt.Frame()
			}
			if features, links := f.count("features"), f.count("links"); features != 1 || links != want[name] {
				t.Fatalf("%d features and %d links requests in 20 frames, want 1 and %d", features, links, want[name])
			}
			notALink(t, tt, app, "reports/a.md")
		})
	}
}

func TestAPathWithPercentAndHashReachesTheDocumentWhole(t *testing.T) {
	rel := "reports/50%/a#b(1).md"
	f := pathFixture(t, map[string]string{rel: rel}, supervisor("open `"+rel+"` now"))
	app, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	click(t, tt, rel)
	if desktop.DocShown(app) != rel {
		t.Fatalf("doc %q, want %q", desktop.DocShown(app), rel)
	}
}

func drainUntil(t *testing.T, posted chan func(), tt *ui.Tester, until func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !until() {
		select {
		case fn := <-posted:
			fn()
			tt.Frame()
		case <-deadline:
			t.Fatal("the state never arrived")
		}
	}
}

func TestAnAnswerForAnotherFleetIsNotAppliedToTheFleetShown(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"}, supervisor("`reports/a.md`"))
	f.timeline[secondID] = f.timeline[fleetID]
	f.status[secondID] = f.status[fleetID]
	f.tasks[secondID] = f.tasks[fleetID]
	f.linkOnly = fleetID
	f.linksGate = make(chan struct{})
	f.linksEntered = make(chan struct{}, 4)
	app := newApp(t, f)
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	<-f.linksEntered
	app.SelectFleet(secondID)
	drainUntil(t, posted, tt, func() bool { return app.Fleet.ID == secondID && len(app.Timeline.Entries) > 0 })
	close(f.linksGate)
	drainUntil(t, posted, tt, func() bool { return !desktop.PathsBusy(app) })
	frames(tt)
	notALink(t, tt, app, "reports/a.md")
	app.SelectFleet(fleetID)
	drainUntil(t, posted, tt, func() bool { return app.Fleet.ID == fleetID && len(app.Timeline.Entries) > 0 })
	frames(tt)
	click(t, tt, "reports/a.md")
	drainUntil(t, posted, tt, func() bool { return desktop.DocShown(app) != "" })
	if desktop.DocShown(app) != "reports/a.md" {
		t.Fatalf("doc %q", desktop.DocShown(app))
	}
}

func TestTheWindowNeverWaitsForTheLinksAnswer(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md"}, supervisor("`reports/a.md`"))
	f.linksGate = make(chan struct{})
	f.linksEntered = make(chan struct{}, 4)
	app := newApp(t, f)
	posted := make(chan func(), 4)
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 1100, 760, false)
	done := make(chan struct{})
	go func() { tt.Frame(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the frame waited for the links answer")
	}
	<-f.linksEntered
	close(f.linksGate)
	(<-posted)()
	frames(tt)
	click(t, tt, "reports/a.md")
	deadline := time.After(10 * time.Second)
	for desktop.DocShown(app) == "" {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatal("the document never opened")
		}
	}
}

func TestARequestStaysInsideTheBoardsQueryLimit(t *testing.T) {
	var b strings.Builder
	links := map[string]string{}
	for i := range 50 {
		p := fmt.Sprintf("reports/%02d-%s", i, strings.Repeat("%", 400))
		fmt.Fprintf(&b, "`%s` ", p)
		links[p] = p
	}
	f := pathFixture(t, nil, supervisor(b.String()))
	_, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	got := f.requests()
	total := 0
	for _, req := range got {
		total += len(req)
		if n := len(url.Values{"path": req}.Encode()); n > 16<<10 || len(req) > 50 {
			t.Fatalf("a request of %d paths is %d bytes of query", len(req), n)
		}
	}
	if len(got) < 2 || total != 50 {
		t.Fatalf("%d requests carry %d paths", len(got), total)
	}
}

func TestAWorkersTranscriptAndADocumentBodyAreNotLinkedForPaths(t *testing.T) {
	f := withAttemptTranscript(viewFixture(t))
	f.features = []string{"links"}
	f.linkTable = map[string]desktop.Link{"reports/a.md": link("reports/a.md")}
	if f.docs == nil {
		f.docs, f.docBytes = map[string]desktop.Doc{}, map[string][]byte{}
	}
	f.docs["reports/note.md"] = desktop.Doc{Path: "reports/note.md", Name: "note.md", Kind: "markdown", Text: true, Size: 5, FolderPage: 1}
	f.docBytes["reports/note.md"] = []byte("# Note\n\nsee `reports/a.md` and /abs/a.md")
	a := f.attempt[fleetID]
	a.Transcript.Turns[1].Text = "Wrote `reports/a.md` and /abs/a.md."
	f.attempt[fleetID] = a
	app := newApp(t, f)
	tt := newTesterAt(app, 1440, 900)
	app.OpenRef("a1")
	frames(tt)
	mustText(t, tt, "Wrote reports/a.md and /abs/a.md.")
	app.OpenDoc("reports/note.md")
	frames(tt)
	mustText(t, tt, "Note")
	if f.count("features") != 0 || f.count("links") != 0 {
		t.Fatalf("asked for links: features %d, links %d", f.count("features"), f.count("links"))
	}
}

func TestAProsePathShowsWholeWhateverPunctuationItHolds(t *testing.T) {
	for _, path := range []string{
		"/abs/a(1).md", "/abs/a[1].md", "/abs/a]b.md", "/abs/a[b.md", "/abs/a_b_c.md",
		"/abs/a&b.md", "/abs/a&amp;b.md", "/abs/a~b~c.md", "/abs/a|b.md", "/abs/a!b.md", "/abs/a!(b).md", `/abs/a\b.md`,
	} {
		for _, c := range pathSizes[:2] {
			t.Run(path+"/"+c.name, func(t *testing.T) {
				f := pathFixture(t, map[string]string{path: "reports/a.md"}, supervisor("Wrote "+path+" now"))
				app, tt := pathApp(t, f, c)
				frames(tt)
				if got := f.requests(); len(got) != 1 || len(got[0]) != 1 || got[0][0] != path {
					t.Fatalf("requests = %q, want the path whole", got)
				}
				mustText(t, tt, "Wrote "+path+" now")
				click(t, tt, path)
				if desktop.DocShown(app) != "reports/a.md" {
					t.Fatalf("doc %q", desktop.DocShown(app))
				}
			})
		}
	}
}

func threeMessagesOfThirtyPaths() (map[string]string, []desktop.Entry) {
	links := map[string]string{}
	var entries []desktop.Entry
	for m := range 3 {
		var b strings.Builder
		for i := range 30 {
			p := fmt.Sprintf("reports/%d-%02d.md", m, i)
			fmt.Fprintf(&b, "`%s` ", p)
			links[p] = p
		}
		e := supervisor(b.String())
		e.No, e.Ref = 9+m, fmt.Sprintf("s%d", 1+m)
		entries = append(entries, e)
	}
	return links, entries
}

func TestARequestCarriesAtMostFiftyPathsAcrossMessagesAndFeaturesAreReadOnce(t *testing.T) {
	links, entries := threeMessagesOfThirtyPaths()
	f := pathFixture(t, links, entries...)
	f.linksGate = make(chan struct{})
	f.linksEntered = make(chan struct{}, 4)
	app := newApp(t, f)
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	<-f.linksEntered
	close(f.linksGate)
	drainUntil(t, posted, tt, func() bool { return len(f.requests()) >= 2 && !desktop.PathsBusy(app) })
	frames(tt)
	got := f.requests()
	total := 0
	for _, req := range got {
		total += len(req)
		if len(req) > 50 {
			t.Fatalf("a request of %d paths", len(req))
		}
	}
	if total != 90 || len(got) != 3 {
		t.Fatalf("%d requests carry %d paths", len(got), total)
	}
	if n := f.count("features"); n != 1 {
		t.Fatalf("%d features reads inside thirty seconds, want 1", n)
	}
	if desktop.PathGen(app) == 0 {
		t.Fatal("the answers changed no link")
	}
}

func TestAnAnswerOfAnotherLengthThanTheRequestLeavesTheTextPlain(t *testing.T) {
	f := pathFixture(t, map[string]string{"reports/a.md": "reports/a.md", "reports/b.md": "reports/b.md"},
		supervisor("`reports/a.md` and `reports/b.md`"))
	f.linksTrim = 1
	app, tt := pathApp(t, f, pathSizes[0])
	frames(tt)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requests", n)
	}
	notALink(t, tt, app, "reports/a.md")
	notALink(t, tt, app, "reports/b.md")
}

func TestTheLinkGenerationMovesOnlyWhenAnAnswerChangesWhatIsShown(t *testing.T) {
	f := pathFixture(t, nil, supervisor("later `reports/a.md`"))
	app := newApp(t, f)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	app.Now = func() time.Time { return now }
	tt := sized(t, app, 1100, 760, false)
	frames(tt)
	if n := len(f.requests()); n != 1 || desktop.PathGen(app) != 0 {
		t.Fatalf("%d requests, generation %d after a new negative", n, desktop.PathGen(app))
	}
	now = now.Add(31 * time.Second)
	tt.Advance(31 * time.Second)
	frames(tt)
	if n := len(f.requests()); n != 2 || desktop.PathGen(app) != 0 {
		t.Fatalf("%d requests, generation %d after a repeated negative", n, desktop.PathGen(app))
	}
	addDoc(f, "reports/a.md")
	f.mu.Lock()
	f.linkTable["reports/a.md"] = link("reports/a.md")
	f.mu.Unlock()
	now = now.Add(31 * time.Second)
	tt.Advance(31 * time.Second)
	frames(tt)
	if desktop.PathGen(app) != 1 {
		t.Fatalf("generation %d after the path became a link, want 1", desktop.PathGen(app))
	}
}

func TestTheQueueOfAFleetIsDroppedWhenAnotherFleetIsShown(t *testing.T) {
	links, entries := threeMessagesOfThirtyPaths()
	f := pathFixture(t, links, entries...)
	empty := f.timeline[fleetID]
	empty.Entries = nil
	f.timeline[secondID] = empty
	f.status[secondID] = f.status[fleetID]
	f.tasks[secondID] = f.tasks[fleetID]
	f.linksGate = make(chan struct{})
	f.linksEntered = make(chan struct{}, 4)
	app := newApp(t, f)
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	<-f.linksEntered
	app.SelectFleet(secondID)
	drainUntil(t, posted, tt, func() bool { return app.Fleet.ID == secondID && len(app.Timeline.Entries) == 0 })
	close(f.linksGate)
	drainUntil(t, posted, tt, func() bool { return !desktop.PathsBusy(app) })
	frames(tt)
	if got := f.requests(); len(got) != 1 {
		t.Fatalf("requests = %d, the queue of the fleet left was asked after the switch", len(got))
	}
}

func TestALinkDiffersFromAPlainSpanInBothThemes(t *testing.T) {
	for _, text := range []string{"Read `reports/a.md` now.", "Read /abs/a.md now."} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%v", text, dark), func(t *testing.T) {
				shot := func(known bool) []byte {
					links := map[string]string{}
					if known {
						links["reports/a.md"], links["/abs/a.md"] = "reports/a.md", "reports/a.md"
					}
					f := pathFixture(t, links, supervisor(text))
					_, tt := pathApp(t, f, pathCase{dark: dark, w: 1100, h: 760})
					frames(tt)
					return tt.Image().Pix
				}
				if bytes.Equal(shot(true), shot(false)) {
					t.Fatal("a path that is a link is drawn like one that is not")
				}
			})
		}
	}
}

func TestScenesPathLinks(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			f := pathFixture(t, map[string]string{
				"reports/standup/2026-11-20.md": "reports/standup/2026-11-20.md",
				"reports/standup/":              "reports/standup",
			}, supervisor("Standup is written: `reports/standup/2026-11-20.md`.\n\nThe folder `reports/standup/` holds the earlier ones; `reports/standup/2026-11-21.md` does not exist yet.\n\n```\nreports/standup/2026-11-20.md\n```\n\nSee t1 for the task."))
			app := newApp(t, f)
			tt := sized(t, app, 1100, 760, dark)
			frames(tt)
			mustText(t, tt, "Standup is written:", "reports/standup/2026-11-20.md")
			shotSized(t, tt, "82-path-links-"+name, 1100, 760)
			click(t, tt, "reports/standup/2026-11-20.md")
			if desktop.DocShown(app) != "reports/standup/2026-11-20.md" {
				t.Fatalf("doc %q", desktop.DocShown(app))
			}
		})
	}
}
