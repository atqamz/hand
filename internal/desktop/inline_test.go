package desktop_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

const inlineText = "look at [attachment 1], as you see there is misalignment, it should be like [attachment 2]"

func inlineFixture(t *testing.T, text string, ns ...int) *fixture {
	t.Helper()
	f := sentFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries[0].Text = text
	for i, n := range ns {
		tl.Entries[0].Shots[i].N = n
	}
	f.timeline[fleetID] = tl
	return f
}

func textTop(t *testing.T, tt *ui.Tester, text string) float32 {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	return r.Y
}

func TestScenesAPictureStandsWhereItsTokenStands(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := inlineFixture(t, inlineText, 1, 2)
			app := newApp(t, f)
			tt := sized(t, app, 1100, 760, dark)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, "look at", ", as you see there is misalignment, it should be like", attachLog)
			mustNotText(t, tt, "[attachment 1]")
			pic := textTop(t, tt, "Attached image "+attachImage)
			first, second, file := textTop(t, tt, "look at"), textTop(t, tt, ", as you see there is misalignment, it should be like"), textTop(t, tt, attachLog)
			if !(first < pic && pic < second && second < file) {
				t.Errorf("order: text %.0f, picture %.0f, text %.0f, file %.0f", first, pic, second, file)
			}
			if got := countText(t, tt, "Attached image "+attachImage); got != 1 {
				t.Errorf("the picture is drawn %d times, want once at its token", got)
			}
			shotSized(t, tt, "83-inline-attachments-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestAShotWithNoTokenGoesBelowTheText(t *testing.T) {
	f := inlineFixture(t, "see [attachment 2]", 1, 2)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	text, file, pic := textTop(t, tt, "see"), textTop(t, tt, attachLog), textTop(t, tt, "Attached image "+attachImage)
	if !(text < file && file < pic) {
		t.Errorf("the file at its token is %.0f, the text %.0f and the picture below it %.0f", file, text, pic)
	}
}

func TestATokenWithNoShotAndAnOldEntryStayAsTheyWere(t *testing.T) {
	f := inlineFixture(t, "keep [attachment 3] and [attachment 0] as text", 1, 2)
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	mustText(t, tt, "[attachment 3]", "[attachment 0]")
	if textTop(t, tt, "Attached image "+attachImage) < textTop(t, tt, "keep [attachment 3] and [attachment 0] as text") {
		t.Error("a picture without a token is not below the text")
	}
	old := inlineFixture(t, "here is the log", 0, 0)
	tt = sized(t, newApp(t, old), 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if textTop(t, tt, "Attached image "+attachImage) < textTop(t, tt, "here is the log") {
		t.Error("an entry without numbers must keep its files below the text")
	}
}

func TestAChipInsertsItsTokenAtTheCaret(t *testing.T) {
	_, img, log := twoFiles(t)
	app := newApp(t, sentFixture(t))
	app.Pick = func() []string { return nil }
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("look at , it should be")
	app.Attach(img, log)
	tt.Frame()
	tt.Frame()
	click(t, tt, "Message the supervisor")
	for range len(", it should be") {
		tt.Key(0, ui.KeyLeft)
	}
	click(t, tt, "Insert [attachment 2]")
	tt.Frame()
	if want := "look at [attachment 2], it should be"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
	tt.Type(" now")
	if want := "look at [attachment 2] now, it should be"; app.Draft != want {
		t.Fatalf("the caret is not after the token: %q, want %q", app.Draft, want)
	}
	click(t, tt, "Insert [attachment 1]")
	tt.Frame()
	if !strings.Contains(app.Draft, "[attachment 1]") || strings.Count(app.Draft, "[attachment") != 2 {
		t.Fatalf("draft %q", app.Draft)
	}
}

func TestAChipInsertsAtTheEndWhenTheBoxWasNeverFocused(t *testing.T) {
	_, img, _ := twoFiles(t)
	app := newApp(t, sentFixture(t))
	tt := sized(t, app, 1100, 760, false)
	app.Draft = "é日 text"
	app.Attach(img)
	tt.Frame()
	tt.Frame()
	click(t, tt, "Insert [attachment 1]")
	tt.Frame()
	if want := "é日 text[attachment 1]"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
}

func TestOnlyAReadyChipOffersInsertAndAnEarlierUploadBlocksIt(t *testing.T) {
	_, img, log := twoFiles(t)
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	app.Pick = func() []string { return nil }
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	app.Attach(img, log)
	tt.Frame()
	mustNotText(t, tt, "Insert")
	drainPosts(t, posted)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Insert [attachment 1]"); !ok {
		t.Fatalf("the first ready chip offers no insert: %q", tt.Texts())
	}
	if _, ok := tt.Find("Insert [attachment 2]"); !ok {
		t.Fatalf("the second ready chip offers no insert: %q", tt.Texts())
	}
}

func TestRemovingAChipDropsItsTokensAndRenumbersTheRest(t *testing.T) {
	_, img, log := twoFiles(t)
	app := newApp(t, sentFixture(t))
	app.Pick = func() []string { return nil }
	tt := sized(t, app, 1100, 760, false)
	app.Draft = "a [attachment 1] b [attachment 2] c [attachment 2] d [attachment 3]"
	app.Attach(img, log)
	tt.Frame()
	tt.Frame()
	click(t, tt, "Remove "+attachImage)
	tt.Frame()
	if want := "a  b [attachment 1] c [attachment 1] d [attachment 2]"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
}

func TestATokenBeyondTheChipsIsFlagged(t *testing.T) {
	_, img, _ := twoFiles(t)
	app := newApp(t, sentFixture(t))
	app.Now = func() time.Time { return time.Date(2026, 11, 1, 11, 41, 0, 0, time.UTC) }
	tt := sized(t, app, 1100, 760, false)
	app.Draft = "see [attachment 2]"
	app.Attach(img)
	tt.Frame()
	tt.Frame()
	mustText(t, tt, "[attachment 2] has no file")
	app.Draft = "see [attachment 1]"
	tt.Frame()
	mustNotText(t, tt, "has no file")
}

func TestScenesTheComposerWithATokenInTheText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			_, img, log := twoFiles(t)
			app := newApp(t, sentFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 11, 41, 0, 0, time.UTC) }
			app.Pick = func() []string { return nil }
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("look at  , it should be like")
			app.Attach(img, log)
			tt.Frame()
			tt.Frame()
			click(t, tt, "Message the supervisor")
			for range len("  , it should be like") - 1 {
				tt.Key(0, ui.KeyLeft)
			}
			click(t, tt, "Insert [attachment 1]")
			tt.Frame()
			tt.Frame()
			mustText(t, tt, "Insert")
			if want := "look at [attachment 1] , it should be like"; app.Draft != want {
				t.Fatalf("draft %q, want %q", app.Draft, want)
			}
			shotSized(t, tt, "84-insert-attachment-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestTheCaretOffsetIsInRunesNotBytes(t *testing.T) {
	_, img, _ := twoFiles(t)
	app := newApp(t, sentFixture(t))
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("é日 x")
	app.Attach(img)
	tt.Frame()
	tt.Frame()
	click(t, tt, "Message the supervisor")
	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyLeft)
	click(t, tt, "Insert [attachment 1]")
	tt.Frame()
	if want := "é日[attachment 1] x"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
}

func TestInsertReplacesTheSelection(t *testing.T) {
	_, img, _ := twoFiles(t)
	app := newApp(t, sentFixture(t))
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	tt.Type("look at XX now")
	app.Attach(img)
	tt.Frame()
	tt.Frame()
	click(t, tt, "Message the supervisor")
	for range len(" now") {
		tt.Key(0, ui.KeyLeft)
	}
	tt.Key(ui.Shift, ui.KeyLeft)
	tt.Key(ui.Shift, ui.KeyLeft)
	click(t, tt, "Insert [attachment 1]")
	tt.Frame()
	if want := "look at [attachment 1] now"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
}

func TestRemovingAChipStillUploadingRenumbersTheTokensOfTheOthers(t *testing.T) {
	_, img, log := twoFiles(t)
	app, _ := asyncApp(t, sentFixture(t))
	tt := sized(t, app, 1100, 760, false)
	app.Draft = "a [attachment 1] b [attachment 2]"
	app.Attach(img, log)
	tt.Frame()
	click(t, tt, "Remove "+attachImage)
	tt.Frame()
	if want := "a  b [attachment 1]"; app.Draft != want {
		t.Fatalf("draft %q, want %q", app.Draft, want)
	}
}

func TestNoInsertWhileAnEarlierChipCouldStillChangeTheNumbers(t *testing.T) {
	dir, img, log := twoFiles(t)
	app := newApp(t, sentFixture(t))
	tt := sized(t, app, 1100, 760, false)
	app.Attach(dir, img)
	tt.Frame()
	tt.Frame()
	mustNotText(t, tt, "Insert [attachment")
	click(t, tt, "Dismiss "+filepath.Base(dir)+"/")
	tt.Frame()
	if _, ok := tt.Find("Insert [attachment 1]"); !ok {
		t.Fatalf("after the failed chip went, the file offers no insert: %q", tt.Texts())
	}
	app.Attach(log)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Insert [attachment 2]"); !ok {
		t.Fatal("the second ready chip offers no insert")
	}
}
