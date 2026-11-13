package desktop_test

import (
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func choiceFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	d := f.decision[fleetID]
	d.Options[0].Recommended = true
	d.Options[0].Note = "reproducible builds; we bump it by hand"
	d.Options[1].Note = "patch fixes arrive by themselves"
	f.decision[fleetID] = d
	return f
}

func choiceShot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("DESKTOP_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	img := tt.Image()
	if img == nil {
		t.Fatal("no frame")
	}
	if img.Bounds().Dx() != windowWidth || img.Bounds().Dy() != windowHeight {
		t.Fatalf("frame is %v, want %dx%d", img.Bounds(), windowWidth, windowHeight)
	}
	f, err := os.Create(filepath.Join(dir, name+".jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
}

func TestScenesChoice(t *testing.T) {
	scenes := []struct {
		name string
		play func(t *testing.T, tt *ui.Tester, f *fixture)
		want []string
	}{
		{"open", nil, []string{"Yes, keep it", "No, rename it", "Recommended", "Send answer"}},
		{"picked", func(t *testing.T, tt *ui.Tester, f *fixture) {
			click(t, tt, "No, rename it")
			click(t, tt, "Note")
			tt.Type("the docs keep the old name")
		}, []string{"No, rename it", "Note", "Send answer"}},
		{"refused", func(t *testing.T, tt *ui.Tester, f *fixture) {
			f.action = &desktop.APIError{Status: 409, Code: "conflict", Message: "decision d1 is answered"}
			click(t, tt, "Yes, keep it")
			click(t, tt, "Send answer")
			tt.Frame()
			tt.Advance(300 * time.Millisecond)
		}, []string{"decision d1 is answered", "Yes, keep it", "Send answer"}},
	}
	for _, dark := range []bool{false, true} {
		mode := map[bool]string{false: "light", true: "dark"}[dark]
		for _, sc := range scenes {
			t.Run(sc.name+"-"+mode, func(t *testing.T) {
				f := choiceFixture(t)
				tt := darkTester(newApp(t, f), dark)
				click(t, tt, "Keep the old cookie name?")
				if sc.play != nil {
					sc.play(t, tt, f)
				}
				tt.Frame()
				choiceShot(t, tt, "choice-"+sc.name+"-"+mode)
				mustText(t, tt, sc.want...)
			})
		}
	}
}

func TestTheChoiceListAnswersInPlaceWithAChoiceAndANote(t *testing.T) {
	f := choiceFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "No, rename it")
	click(t, tt, "Note")
	tt.Type("the docs keep the old name")
	click(t, tt, "Send answer")
	if f.choice != "2. No, rename it" || f.answerNote != "the docs keep the old name" {
		t.Fatalf("sent choice %q note %q", f.choice, f.answerNote)
	}
}

func TestAnOtherOptionWithoutANoteMakesNoRequest(t *testing.T) {
	f := choiceOtherFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	f.action = &desktop.APIError{Status: 400, Code: "bad_request", Message: "write your answer: that option asks for one"}
	click(t, tt, "Other")
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "Write your answer: that option asks for one.", "Send answer")
	mustNotText(t, tt, "Sent 3 · waiting…")
	if n := f.count("answer:d1"); n != 0 {
		t.Errorf("the refused answer was posted %d times", n)
	}
	if !tt.Focused("Note") {
		t.Error("the note does not have the focus")
	}
	if app.Screen != desktop.BoardScreen {
		t.Fatalf("the refusal left the Board screen for %v", app.Screen)
	}
}

func TestEnterOnAnOtherOptionWithoutANoteMakesNoRequest(t *testing.T) {
	f := choiceOtherFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	app.OpenRef("d1")
	app.AnswerChoice = "3. Other"
	f.action = &desktop.APIError{Status: 400, Code: "bad_request", Message: "write your answer: that option asks for one"}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	mustText(t, tt, "Write your answer: that option asks for one.", "Send answer")
	mustNotText(t, tt, "Sent 3 · waiting…")
	if n := f.count("answer:d1"); n != 0 {
		t.Errorf("Enter posted the refused answer %d times", n)
	}
	if !tt.Focused("Note") {
		t.Error("Enter did not reach the guard and focus the note")
	}
}

func choiceOtherFixture(t *testing.T) *fixture {
	t.Helper()
	f := goldenFixture(t)
	d := f.decision[fleetID]
	d.Options = append(d.Options, desktop.Option{Key: "3", Label: "Other", Note: "write your own", Value: "3. Other", Other: true})
	f.decision[fleetID] = d
	return f
}

func TestAnEmptyAnswerIsRefusedInTheReceipt(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	f.action = &desktop.APIError{Status: 400, Code: "bad_request", Message: "pick an option or write an answer"}
	click(t, tt, "Send answer")
	tt.Frame()
	mustText(t, tt, "pick an option or write an answer", "Send answer")
	mustNotText(t, tt, "Sent")
	if n := f.count("answer:d1"); n != 1 {
		t.Errorf("the empty answer was posted %d times, want one server refusal", n)
	}
}

func TestAnAnsweredDecisionSaysSentUntilTheStatusRemovesIt(t *testing.T) {
	f := goldenFixture(t)
	f.keepNeeds = true
	tt := newTester(newApp(t, f))
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Yes, keep it")
	click(t, tt, "Send answer")
	mustText(t, tt, "Sent 1 · waiting…")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "Sent 1 · waiting…")
	mustNotText(t, tt, "Send answer")

	st := f.status[fleetID]
	kept := st.Needs.Items[:0]
	for _, n := range st.Needs.Items {
		if n.Decision != "d1" {
			kept = append(kept, n)
		}
	}
	st.Needs.Items = kept
	st.Needs.Waiting = len(kept)
	f.status[fleetID] = st
	menu(t, tt, "Refresh now")
	mustNotText(t, tt, "Sent 1 · waiting…")
	mustNotText(t, tt, "Keep the old cookie name?")
}

func TestNumberKeysPickAnOptionAndEnterSends(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	tt.Key(0, ui.Key2)
	tt.Frame()
	if app.AnswerChoice != "2. No, rename it" {
		t.Fatalf("2 picked %q", app.AnswerChoice)
	}
	tt.Key(0, ui.Key1)
	tt.Frame()
	if app.AnswerChoice != "1. Yes, keep it" {
		t.Fatalf("1 picked %q", app.AnswerChoice)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if f.count("answer:d1") != 1 || f.choice != "1. Yes, keep it" {
		t.Fatalf("Enter sent %d answers, choice %q", f.count("answer:d1"), f.choice)
	}
}

func TestNumberKeysDoNotFireWhileTheNoteHasFocus(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Note")
	tt.Key(0, ui.Key2)
	tt.Frame()
	if app.AnswerChoice != "" {
		t.Fatalf("a key typed in the note picked %q", app.AnswerChoice)
	}
	tt.Type("2")
	tt.Frame()
	if app.AnswerNote != "2" {
		t.Errorf("the note is %q, want the typed 2", app.AnswerNote)
	}
}

func TestNumberKeysDoNotFireWhileTheComposerHasFocus(t *testing.T) {
	f := goldenFixture(t)
	app := newApp(t, f)
	tt := newTester(app)
	click(t, tt, "Keep the old cookie name?")
	click(t, tt, "Message the supervisor")
	tt.Key(0, ui.Key1)
	tt.Frame()
	if app.AnswerChoice != "" {
		t.Fatalf("a key typed in the composer picked %q", app.AnswerChoice)
	}
	tt.Type("hello")
	tt.Key(ui.Ctrl, ui.KeyEnter)
	tt.Frame()
	if f.count("send:"+fleetID) != 1 {
		t.Errorf("Ctrl+Enter sent %d messages, want one", f.count("send:"+fleetID))
	}
	if f.count("answer:d1") != 0 {
		t.Errorf("Ctrl+Enter answered the decision %d times", f.count("answer:d1"))
	}
}

func TestTheRecommendedOptionCarriesItsBadge(t *testing.T) {
	f := choiceFixture(t)
	tt := newTester(newApp(t, f))
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "Recommended")
	if n := countText(t, tt, "Recommended"); n != 1 {
		t.Errorf("Recommended is shown %d times, want once", n)
	}
}
