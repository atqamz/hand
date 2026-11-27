package desktop_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type fakePlayer struct {
	mu      sync.Mutex
	playing bool
	pos     time.Duration
	dur     time.Duration
	plays   int
	pauses  int
	closed  int
	seeks   []time.Duration
}

func (p *fakePlayer) Play()  { p.mu.Lock(); p.playing = true; p.plays++; p.mu.Unlock() }
func (p *fakePlayer) Pause() { p.mu.Lock(); p.playing = false; p.pauses++; p.mu.Unlock() }
func (p *fakePlayer) Seek(d time.Duration) {
	p.mu.Lock()
	p.pos = d
	p.seeks = append(p.seeks, d)
	p.mu.Unlock()
}
func (p *fakePlayer) Position() time.Duration { p.mu.Lock(); defer p.mu.Unlock(); return p.pos }
func (p *fakePlayer) Duration() time.Duration { p.mu.Lock(); defer p.mu.Unlock(); return p.dur }
func (p *fakePlayer) Playing() bool           { p.mu.Lock(); defer p.mu.Unlock(); return p.playing }
func (p *fakePlayer) Close()                  { p.mu.Lock(); p.closed++; p.playing = false; p.mu.Unlock() }
func (p *fakePlayer) closes() int             { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }

type fakeAudio struct {
	mu      sync.Mutex
	players []*fakePlayer
	err     error
	dur     time.Duration
	sizes   []int
}

func (a *fakeAudio) open(b []byte) (desktop.AudioPlayer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sizes = append(a.sizes, len(b))
	if a.err != nil {
		return nil, a.err
	}
	p := &fakePlayer{dur: a.dur}
	a.players = append(a.players, p)
	return p, nil
}

func (a *fakeAudio) made() []*fakePlayer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*fakePlayer(nil), a.players...)
}

func (a *fakeAudio) opens() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sizes)
}

func audioApp(t *testing.T, f *fixture, fa *fakeAudio) *desktop.App {
	t.Helper()
	app := newApp(t, f)
	app.SaveAs = func(string) string { return "" }
	app.OpenAudio = fa.open
	return app
}

func TestAnAudioFileGetsAPlayerWithItsLengthAndControls(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		fa := &fakeAudio{dur: 125 * time.Second}
		app := audioApp(t, kindsFixture(t), fa)
		app.OpenDoc(voicePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "standup.mp3", "Play", "0:00", "2:05")
		mustNotText(t, tt, "Pause")
		if got := len(fa.made()); got != 1 {
			t.Fatalf("players opened = %d, want 1", got)
		}
		p := fa.made()[0]
		click(t, tt, "Play")
		if p.plays != 1 {
			t.Fatalf("plays = %d, want 1", p.plays)
		}
		tt.Frame()
		mustText(t, tt, "Pause")
		mustNotText(t, tt, "No sound output is available")
		click(t, tt, "Pause")
		if p.pauses != 1 {
			t.Fatalf("pauses = %d, want 1", p.pauses)
		}
	})
}

func TestTheAudioPositionTextFollowsThePlayer(t *testing.T) {
	fa := &fakeAudio{dur: 3725 * time.Second}
	app := audioApp(t, kindsFixture(t), fa)
	app.OpenDoc(voicePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustText(t, tt, "1:02:05")
	click(t, tt, "Play")
	p := fa.made()[0]
	p.mu.Lock()
	p.pos = 65 * time.Second
	p.mu.Unlock()
	tt.Advance(time.Second)
	tt.Frame()
	mustText(t, tt, "1:05")
}

func TestAPlayedAudioFileOffersToPlayAgainAtItsEnd(t *testing.T) {
	fa := &fakeAudio{dur: 10 * time.Second}
	app := audioApp(t, kindsFixture(t), fa)
	app.OpenDoc(voicePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	p := fa.made()[0]
	click(t, tt, "Play")
	p.mu.Lock()
	p.pos, p.playing = 10*time.Second, false
	p.mu.Unlock()
	tt.Advance(time.Second)
	tt.Frame()
	mustText(t, tt, "Play again", "0:10")
	click(t, tt, "Play again")
	if len(p.seeks) != 1 || p.seeks[0] != 0 || p.plays != 2 {
		t.Fatalf("seeks = %v, plays = %d, want one seek to 0 and a second play", p.seeks, p.plays)
	}
}

func TestTheAudioPlayerIsReleasedWhenTheWindowLeavesIt(t *testing.T) {
	for name, leave := range map[string]func(*desktop.App){
		"back":         func(a *desktop.App) { a.Back() },
		"close viewer": func(a *desktop.App) { a.CloseViewer() },
		"other file":   func(a *desktop.App) { a.OpenDoc("reports/t1/design.md") },
		"other fleet":  func(a *desktop.App) { a.SelectFleet(secondID) },
		"window close": func(a *desktop.App) { a.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			fa := &fakeAudio{dur: time.Minute}
			app := audioApp(t, kindsFixture(t), fa)
			app.OpenDoc(voicePath)
			tt := sized(t, app, windowWidth, windowHeight, false)
			click(t, tt, "Play")
			p := fa.made()[0]
			if p.closes() != 0 {
				t.Fatalf("closed %d times while the page is open", p.closes())
			}
			leave(app)
			tt.Frame()
			if p.closes() != 1 || p.Playing() {
				t.Fatalf("closes = %d, playing = %v after leaving, want 1 and false", p.closes(), p.Playing())
			}
		})
	}
}

func TestTheAudioPlayerIsReleasedBeforeTheNextFileHasBeenRead(t *testing.T) {
	fa := &fakeAudio{dur: time.Minute}
	app := audioApp(t, kindsFixture(t), fa)
	app.OpenDoc(voicePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "Play")
	p := fa.made()[0]
	var held []func()
	app.Post = func(fn func()) { held = append(held, fn) }
	app.OpenDoc("reports/t1/design.md")
	if p.closes() != 1 || p.Playing() {
		t.Fatalf("closes = %d, playing = %v before the next file is read, want 1 and false", p.closes(), p.Playing())
	}
}

func TestReloadingAnAudioPageKeepsItsPlayer(t *testing.T) {
	fa := &fakeAudio{dur: time.Minute}
	app := audioApp(t, kindsFixture(t), fa)
	app.OpenDoc(voicePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "Play")
	app.Reload()
	tt.Frame()
	if fa.opens() != 1 || fa.made()[0].closes() != 0 || !fa.made()[0].Playing() {
		t.Fatalf("a reload replaced the player: opens = %d, closes = %d", fa.opens(), fa.made()[0].closes())
	}
}

func TestAnAudioFetchErrorClearsWhenRetrySucceeds(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		f := kindsFixture(t)
		f.docErr = map[string]error{voicePath: errors.New("the board is busy")}
		fa := &fakeAudio{dur: time.Minute}
		app := audioApp(t, f, fa)
		app.OpenDoc(voicePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "Retry")
		mustNotText(t, tt, "Play")
		if fa.opens() != 0 {
			t.Fatalf("opened a player after a failed fetch")
		}
		f.mu.Lock()
		delete(f.docErr, voicePath)
		f.mu.Unlock()
		click(t, tt, "Retry")
		tt.Frame()
		mustText(t, tt, "Play", "1:00")
		mustNotText(t, tt, "Retry")
		mustNotText(t, tt, "the board is busy")
	})
}

func TestAnAudioFileInAnUnplayableFormatShowsTheNoticeAndTheSavePath(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		fa := &fakeAudio{err: errors.New("unknown format")}
		app := audioApp(t, kindsFixture(t), fa)
		app.OpenDoc(voicePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		mustText(t, tt, "standup.mp3", "This audio file cannot be played in this window. It plays WAV, MP3 and Ogg Vorbis; download it to play it elsewhere.", "Download")
		mustNotText(t, tt, "Play")
	})
}

func TestTheAudioSizeCapIsExact(t *testing.T) {
	const limit = 10 << 20
	for _, tc := range []struct {
		name     string
		size     int64
		body     int
		wantOpen bool
	}{
		{"size at the cap", limit, 16, true},
		{"size one over", limit + 1, 16, false},
		{"bytes at the cap", 16, limit, true},
		{"bytes one over", 16, limit + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := kindsFixture(t)
			d := f.docs[voicePath]
			d.Size = tc.size
			f.docs[voicePath] = d
			f.docBytes[voicePath] = make([]byte, tc.body)
			fa := &fakeAudio{dur: time.Minute}
			app := audioApp(t, f, fa)
			app.OpenDoc(voicePath)
			tt := sized(t, app, windowWidth, windowHeight, false)
			if got := fa.opens() == 1; got != tc.wantOpen {
				t.Fatalf("opened = %v, want %v", got, tc.wantOpen)
			}
			if tc.wantOpen {
				mustText(t, tt, "Play")
				return
			}
			mustText(t, tt, "This audio file is larger than this window reads; download it to play it elsewhere.", "Download")
			mustNotText(t, tt, "Play")
		})
	}
}

func TestAnAudioAnswerThatArrivesAfterLeavingIsIgnoredAndReleased(t *testing.T) {
	fa := &fakeAudio{dur: time.Minute}
	app := audioApp(t, kindsFixture(t), fa)
	posted := make(chan func(), 16)
	app.Post = func(fn func()) { posted <- fn }
	app.OpenDoc(voicePath)
	deadline := time.After(5 * time.Second)
	for fa.opens() == 0 {
		select {
		case fn := <-posted:
			if fa.opens() > 0 {
				posted <- fn
			}
		case <-deadline:
			t.Fatal("the window never read the file")
		case <-time.After(time.Millisecond):
		}
	}
	app.CloseViewer()
	var held []func()
	for len(held) == 0 {
		select {
		case fn := <-posted:
			held = append(held, fn)
		case <-deadline:
			t.Fatal("the answer never arrived")
		}
	}
drain:
	for {
		select {
		case fn := <-posted:
			held = append(held, fn)
		case <-time.After(20 * time.Millisecond):
			break drain
		}
	}
	for _, fn := range held {
		fn()
	}
	tt := sized(t, app, windowWidth, windowHeight, false)
	mustNotText(t, tt, "Play")
	p := fa.made()[0]
	if p.closes() != 1 {
		t.Fatalf("the late player was closed %d times, want 1", p.closes())
	}
}

func TestNoSoundOutputIsSaidOnScreenOncePlayHasBeenPressed(t *testing.T) {
	both(t, func(t *testing.T, dark bool) {
		fa := &fakeAudio{dur: time.Minute}
		app := audioApp(t, kindsFixture(t), fa)
		var fault error
		app.AudioErr = func() error { return fault }
		app.OpenDoc(voicePath)
		tt := sized(t, app, windowWidth, windowHeight, dark)
		fault = errors.New("no audio device")
		tt.Frame()
		mustNotText(t, tt, "No sound output is available")
		click(t, tt, "Play")
		tt.Frame()
		mustText(t, tt, "No sound output is available, so the file plays unheard: no audio device")
		fault = nil
		tt.Frame()
		mustNotText(t, tt, "No sound output is available")
	})
}

func TestTheAudioSliderSeeksThePlayer(t *testing.T) {
	fa := &fakeAudio{dur: 200 * time.Second}
	app := audioApp(t, kindsFixture(t), fa)
	app.OpenDoc(voicePath)
	tt := sized(t, app, windowWidth, windowHeight, false)
	r, ok := tt.Find("Position")
	if !ok || r.W == 0 {
		t.Fatalf("no slider in %q", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	p := fa.made()[0]
	if len(p.seeks) == 0 {
		t.Fatal("clicking the slider did not seek")
	}
	got := p.seeks[len(p.seeks)-1]
	if got < 80*time.Second || got > 120*time.Second {
		t.Fatalf("seek = %v, want about the middle of 3:20", got)
	}
}
