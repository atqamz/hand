package desktop

import (
	"bytes"
	"fmt"
	"time"

	"github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/mp3"
	_ "github.com/egoist/mygo/audio/vorbis"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	audioTick     = 250 * time.Millisecond
	audioTooLarge = "This audio file is larger than this window reads; download it to play it elsewhere."
	audioFormat   = "This audio file cannot be played in this window. It plays WAV, MP3 and Ogg Vorbis; download it to play it elsewhere."
)

type AudioPlayer interface {
	Play()
	Pause()
	Seek(time.Duration)
	Position() time.Duration
	Duration() time.Duration
	Playing() bool
	Close()
}

type audioState struct {
	path   string
	player AudioPlayer
	pos    float64
	played bool
}

func openAudio(b []byte) (AudioPlayer, error) {
	p, err := audio.OpenStream(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (a *App) audioOpener() func([]byte) (AudioPlayer, error) {
	if a.OpenAudio != nil {
		return a.OpenAudio
	}
	return openAudio
}

func openedAudio(open func([]byte) (AudioPlayer, error), b []byte) (AudioPlayer, string) {
	if len(b) > docRawMax {
		return nil, audioTooLarge
	}
	p, err := open(b)
	if err != nil || p == nil {
		return nil, audioFormat
	}
	return p, ""
}

func (a *App) audioFault() error {
	if a.AudioErr != nil {
		return a.AudioErr()
	}
	return audio.Err()
}

func (a *App) releaseMedia() {
	if p := a.au.player; p != nil {
		p.Close()
	}
	a.au = audioState{}
	if a.web.handle.Close != nil {
		a.web.handle.Close()
	}
	a.web = webState{}
	a.webGen++
}

func (a *App) audioHeld(rel string) bool {
	return a.au.player != nil && a.au.path == rel
}

func (a *App) docAudio(c *ui.Context, k kit.Colors, p palette) {
	st := &a.doc
	if st.row != "" {
		a.docRow(c, k, st.row, false)
		return
	}
	au := &a.au
	if au.player == nil || au.path != a.Ref {
		a.quiet(c, p, "Reading…")
		return
	}
	pl := au.player
	playing := pl.Playing()
	dur := pl.Duration()
	au.pos = pl.Position().Seconds()
	if playing {
		c.After(audioTick)
	}
	ended := !playing && dur > 0 && au.pos >= dur.Seconds()-0.05
	label := "Play"
	switch {
	case playing:
		label = "Pause"
	case ended:
		label = "Play again"
	}
	ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Label("Audio player").Children(func() {
		if kit.Button(c, k, "doc:audio:toggle", label, 96, false) {
			a.audioToggle(playing, ended)
		}
		ui.Text(c, clock(time.Duration(au.pos*float64(time.Second)))).FontSize(kit.Body.Size).FontFeatures("tnum")
		slider := ui.Slider(c, &au.pos, 0, max(dur.Seconds(), 1)).Grow(1).MinWidth(120).Label("Position").Disabled(dur <= 0)
		if slider.Changed() {
			pl.Seek(time.Duration(au.pos * float64(time.Second)))
		}
		ui.Text(c, clock(dur)).FontSize(kit.Body.Size).TextColor(p.neutral).FontFeatures("tnum")
	})
	if au.played {
		if err := a.audioFault(); err != nil {
			a.docRow(c, k, "No sound output is available, so the file plays unheard: "+shownText(err.Error()), false)
		}
	}
}

func (a *App) audioToggle(playing, ended bool) {
	au := &a.au
	if au.player == nil {
		return
	}
	switch {
	case playing:
		au.player.Pause()
	default:
		if ended {
			au.player.Seek(0)
			au.pos = 0
		}
		au.player.Play()
		au.played = true
	}
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
