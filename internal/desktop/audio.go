package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/mp3"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	audioTick     = 250 * time.Millisecond
	audioTooLarge = "This audio file is larger than this window reads; download it to play it elsewhere."
	audioMaxRate  = 384000
	audioMaxChans = 8
	audioFormat   = "This audio file cannot be played in this window. It plays WAV and MP3; download it to play it elsewhere."
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

var errAudioRate = errors.New("the audio sample rate is not between 1 and 384000 Hz")

var errAudioChannels = errors.New("the audio channel count is not between 1 and 8")

func openAudio(b []byte) (AudioPlayer, error) {
	if bytes.HasPrefix(b, []byte("OggS")) {
		return nil, audio.ErrFormat
	}
	d, _, err := audio.NewDecoder(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if r := d.SampleRate(); r < 1 || r > audioMaxRate {
		return nil, errAudioRate
	}
	if c := d.Channels(); c < 1 || c > audioMaxChans {
		return nil, errAudioChannels
	}
	return audio.NewPlayer(d), nil
}

func (a *App) audioOpener() func([]byte) (AudioPlayer, error) {
	if a.OpenAudio != nil {
		return a.OpenAudio
	}
	return openAudio
}

func openedAudio(open func([]byte) (AudioPlayer, error), b []byte) (p AudioPlayer, row string) {
	if len(b) > docRawMax {
		return nil, audioTooLarge
	}
	defer func() {
		if recover() != nil {
			p, row = nil, audioFormat
		}
	}()
	pl, err := open(b)
	if err != nil || pl == nil {
		return nil, audioFormat
	}
	return pl, ""
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
		ui.Text(c, clock(time.Duration(au.pos*float64(time.Second)))).Font(kit.Mono).FontSize(kit.Body.Size).FontFeatures("tnum")
		slider := ui.Slider(c, &au.pos, 0, max(dur.Seconds(), 1)).Grow(1).MinWidth(120).Label("Position").Disabled(dur <= 0)
		if slider.Changed() {
			pl.Seek(time.Duration(au.pos * float64(time.Second)))
		}
		ui.Text(c, clock(dur)).Font(kit.Mono).FontSize(kit.Body.Size).TextColor(p.neutral).FontFeatures("tnum")
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
