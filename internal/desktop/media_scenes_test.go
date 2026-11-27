package desktop_test

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func mediaScene(t *testing.T, base, path string, prepare func(*desktop.App),
	drive func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool)) {
	t.Helper()
	pinZone(t)
	for _, dark := range []bool{false, true} {
		name := base + "-1100x760-light"
		if dark {
			name = base + "-1100x760-dark"
		}
		t.Run(name, func(t *testing.T) {
			app := webApp(t, &framedModel{fixture: kindsFixture(t)}, nil)
			prepare(app)
			app.OpenDoc(path)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			drive(t, app, tt, dark)
			shotSized(t, tt, name, windowWidth, windowHeight)
		})
	}
}

func withAudio(fa **fakeAudio, dur time.Duration) func(*desktop.App) {
	return func(app *desktop.App) {
		*fa = &fakeAudio{dur: dur}
		app.OpenAudio = (*fa).open
	}
}

func TestScenesAnAudioFileIdle(t *testing.T) {
	var fa *fakeAudio
	mediaScene(t, "130-doc-audio-idle", voicePath, withAudio(&fa, 125*time.Second), func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		mustText(t, tt, "standup.mp3", "3.0 MiB", "Play", "0:00", "2:05", "Download", "Copy path")
		for _, s := range []string{"Play", "0:00", "2:05"} {
			inked(t, tt, s)
		}
	})
}

func TestScenesAnAudioFilePlaying(t *testing.T) {
	var fa *fakeAudio
	mediaScene(t, "131-doc-audio-playing", voicePath, withAudio(&fa, 125*time.Second), func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		click(t, tt, "Play")
		p := fa.made()[0]
		p.mu.Lock()
		p.pos = 42 * time.Second
		p.mu.Unlock()
		tt.Advance(time.Second)
		tt.Frame()
		mustText(t, tt, "Pause", "0:42", "2:05")
		mustNotText(t, tt, "No sound output is available")
		for _, s := range []string{"Pause", "0:42", "2:05"} {
			inked(t, tt, s)
		}
	})
}

func TestScenesAnAudioFileWithNoSoundOutput(t *testing.T) {
	const sentence = "No sound output is available, so the file plays unheard: no audio output device"
	var fa *fakeAudio
	mediaScene(t, "132-doc-audio-no-output", voicePath, func(app *desktop.App) {
		withAudio(&fa, 125*time.Second)(app)
		app.AudioErr = func() error { return errors.New("no audio output device") }
	}, func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
		click(t, tt, "Play")
		tt.Advance(time.Second)
		tt.Frame()
		mustText(t, tt, sentence, "Pause")
		inked(t, tt, sentence)
	})
}

func TestScenesAPDFHasAWebViewPlaceholder(t *testing.T) {
	w := &fakeWeb{}
	mediaScene(t, "133-doc-pdf-placeholder", planPath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			mustText(t, tt, "plan.pdf", "51 KiB", pdfPlaceholder, "Document view", "Download", "Copy path")
			inked(t, tt, pdfPlaceholder)
		})
}

func TestScenesAPDFWithNoWebEngine(t *testing.T) {
	w := &fakeWeb{err: errors.New("libwebkit2gtk-4.1.so.0: cannot open shared object file")}
	mediaScene(t, "134-doc-pdf-no-engine", planPath, func(app *desktop.App) { app.OpenWeb = w.open },
		func(t *testing.T, app *desktop.App, tt *ui.Tester, dark bool) {
			notice(t, tt, dark, engineMissing)
			mustText(t, tt, "plan.pdf", "51 KiB")
		})
}
