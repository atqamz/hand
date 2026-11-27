package desktop

import (
	"encoding/binary"
	"errors"
	"github.com/egoist/mygo/audio"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func oggCRC() *crc32.Table {
	var t crc32.Table
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return &t
}

func repairOggPages(b []byte) {
	tab := oggCRC()
	for i := 0; i+27 <= len(b) && string(b[i:i+4]) == "OggS"; {
		n := int(b[i+26])
		if i+27+n > len(b) {
			return
		}
		size := 27 + n
		for _, s := range b[i+27 : i+27+n] {
			size += int(s)
		}
		if i+size > len(b) {
			return
		}
		clear(b[i+22 : i+26])
		var c uint32
		for _, x := range b[i : i+size] {
			c = c<<8 ^ tab[byte(c>>24)^x]
		}
		b[i+22], b[i+23], b[i+24], b[i+25] = byte(c), byte(c>>8), byte(c>>16), byte(c>>24)
		i += size
	}
}

func TestMutatedOggVorbisFilesNeverReachAPlayerThatPanics(t *testing.T) {
	base, err := os.ReadFile("testdata/tone.ogg")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	opened, refused := 0, 0
	for range 400 {
		b := append([]byte(nil), base...)
		for k := 1 + rng.Intn(3); k > 0; k-- {
			b[rng.Intn(len(b))] ^= byte(1 << rng.Intn(8))
		}
		repairOggPages(b)
		p, row := openedAudio(openAudio, b)
		if p != nil {
			opened++
			p.Close()
			continue
		}
		if row != audioFormat {
			t.Fatalf("a mutated file got the row %q", row)
		}
		refused++
	}
	if opened == 0 || refused == 0 {
		t.Fatalf("opened = %d, refused = %d: the mutations do not cover both outcomes", opened, refused)
	}
}

func TestTheOggVorbisCheckRefusesWhatPanicsTheDecoder(t *testing.T) {
	for _, name := range []string{"ogg-header-panic.ogg", "ogg-decode-panic.ogg"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if p, err := openAudio(b); p != nil || err == nil {
			t.Fatalf("%s: player = %v, err = %v, want no player and an error", name, p, err)
		}
	}
}

func oggOffsets(b []byte) []int {
	var offs []int
	for i := 0; i+27 <= len(b) && string(b[i:i+4]) == "OggS"; {
		offs = append(offs, i)
		size := 27 + int(b[i+26])
		for _, s := range b[i+27 : i+27+int(b[i+26])] {
			size += int(s)
		}
		i += size
	}
	return offs
}

func editedOgg(t *testing.T, edit func(b []byte, pages []int)) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/multi.ogg")
	if err != nil {
		t.Fatal(err)
	}
	edit(b, oggOffsets(b))
	repairOggPages(b)
	return b
}

func setGranule(b []byte, page int, g int64) {
	binary.LittleEndian.PutUint64(b[page+6:], uint64(g))
}

func granuleOf(b []byte, page int) int64 {
	return int64(binary.LittleEndian.Uint64(b[page+6:]))
}

func TestTheOggVorbisFixtureWithAnEarlyEndOfStreamIsRefused(t *testing.T) {
	b, err := os.ReadFile("testdata/ogg-early-eos.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oggFrames(b, openOggDecoder); err != nil {
		t.Fatalf("the fixture must decode linearly, so that only its structure refuses it: %v", err)
	}
	if p, err := openAudio(b); p != nil || err == nil {
		t.Fatalf("player = %v, err = %v, want no player and an error", p, err)
	}
	if p, row := openedAudio(openAudio, b); p != nil || row != audioFormat {
		t.Fatalf("player = %v, row = %q, want the cannot-play sentence", p, row)
	}
}

func TestEachStructuralCheckOfAnOggVorbisFileRefusesOnItsOwn(t *testing.T) {
	cases := []struct {
		name string
		edit func(b []byte, pages []int)
	}{
		{"an end of stream flag before the last page", func(b []byte, pages []int) {
			b[pages[5]+5] |= oggEOS
			for _, p := range pages[6:] {
				setGranule(b, p, granuleOf(b, pages[5]))
			}
		}},
		{"a second logical stream", func(b []byte, pages []int) {
			binary.LittleEndian.PutUint32(b[pages[len(pages)-1]+14:], 0x1234)
		}},
		{"a granule position that decreases", func(b []byte, pages []int) {
			setGranule(b, pages[8], granuleOf(b, pages[7])-1)
		}},
		{"a granule position beyond the frames decoded", func(b []byte, pages []int) {
			setGranule(b, pages[len(pages)-1], granuleOf(b, pages[len(pages)-1])+4000)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := editedOgg(t, c.edit)
			ps, ok := oggPages(b)
			if !ok {
				t.Fatal("the edit broke the page layout")
			}
			frames, err := oggFrames(b, openOggDecoder)
			if err != nil {
				t.Fatalf("the file must decode linearly, so that only its structure refuses it: %v", err)
			}
			if oggStructure(ps, frames) {
				t.Fatal("the structure check accepted it")
			}
			if p, err := openAudio(b); p != nil || err == nil {
				t.Fatalf("player = %v, err = %v, want no player and an error", p, err)
			}
		})
	}
}

func TestAnUntouchedOggVorbisFileOfSeveralSecondsPassesTheStructureCheck(t *testing.T) {
	b := editedOgg(t, func([]byte, []int) {})
	ps, ok := oggPages(b)
	if !ok || len(ps) < 10 {
		t.Fatalf("pages = %d, ok = %v, want a file of many pages", len(ps), ok)
	}
	frames, err := oggFrames(b, openOggDecoder)
	if err != nil || frames != 64000 || !oggStructure(ps, frames) {
		t.Fatalf("frames = %d, err = %v, structure = %v", frames, err, oggStructure(ps, frames))
	}
}

type spyOggDecoder struct {
	audio.Decoder
	t      *testing.T
	setPos *int
}

func (s spyOggDecoder) SetPosition(int64) error {
	*s.setPos++
	s.t.Error("the decoder's own SetPosition was called")
	return errors.New("not allowed")
}

type fakeOggDecoder struct {
	ch     int
	frames int64
	after  error
	done   int64
}

func (f *fakeOggDecoder) SampleRate() int         { return 16000 }
func (f *fakeOggDecoder) Channels() int           { return f.ch }
func (f *fakeOggDecoder) SetPosition(int64) error { return nil }
func (f *fakeOggDecoder) Len() int64              { return f.frames }
func (f *fakeOggDecoder) Read(buf []float32) (int, error) {
	if f.done >= f.frames {
		return 0, f.after
	}
	n := min(int64(len(buf)/f.ch), f.frames-f.done)
	clear(buf[:n*int64(f.ch)])
	f.done += n
	return int(n) * f.ch, nil
}

func fakeOggOpen(ch int, frames int64, after error) func([]byte) (audio.Decoder, error) {
	return func([]byte) (audio.Decoder, error) { return &fakeOggDecoder{ch: ch, frames: frames, after: after}, nil }
}

func multiOgg(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/multi.ogg")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var oggSeekSequence = []float64{0.5, 0.0, 0.9, 0.999, 1.5, 0.3, 0.7}

func TestADecodeErrorWithoutAPanicNeverOpensAPlayer(t *testing.T) {
	b := multiOgg(t)
	boom := errors.New("boom")
	p, err := openOggWith(b, fakeOggOpen(1, 64000, boom))
	if p != nil || err != boom {
		t.Fatalf("player = %v, err = %v, want no player and the decoder's error", p, err)
	}
	p, err = openOggWith(b, fakeOggOpen(1, 64000, io.EOF))
	if err != nil || p == nil {
		t.Fatalf("player = %v, err = %v, want a player when the decode ends cleanly", p, err)
	}
	p.Close()
}

func TestADecoderThatReadsNothingWithoutAnErrorNeverOpensAPlayer(t *testing.T) {
	if p, err := openOggWith(multiOgg(t), fakeOggOpen(1, 0, nil)); p != nil || err == nil {
		t.Fatalf("player = %v, err = %v, want no player and an error", p, err)
	}
}

func TestOnlyOneTo255ChannelsOpenAPlayer(t *testing.T) {
	b := multiOgg(t)
	for _, ch := range []int{0, 256, -1} {
		if p, err := openOggWith(b, fakeOggOpen(ch, 64000, io.EOF)); p != nil || err == nil {
			t.Fatalf("%d channels: player = %v, err = %v, want no player and an error", ch, p, err)
		}
	}
	for _, ch := range []int{1, 2, 255} {
		p, err := openOggWith(b, fakeOggOpen(ch, 64000, io.EOF))
		if err != nil || p == nil {
			t.Fatalf("%d channels: player = %v, err = %v, want a player", ch, p, err)
		}
		p.Close()
	}
}

func TestSeekingAnOggVorbisFileNeverCallsTheDecodersSetPosition(t *testing.T) {
	b := multiOgg(t)
	calls, opens := 0, 0
	open := func(b []byte) (audio.Decoder, error) {
		opens++
		d, err := openOggDecoder(b)
		return spyOggDecoder{d, t, &calls}, err
	}
	d, err := newOggDecoder(b, 64000, open)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 700)
	for _, f := range oggSeekSequence {
		if err := d.SetPosition(int64(f * 64000)); err != nil {
			t.Fatal(err)
		}
		d.Read(buf)
	}
	if calls != 0 || opens < 3 {
		t.Fatalf("SetPosition calls = %d, opens = %d, want none and a reopen for each seek back", calls, opens)
	}
}

func TestSeeksInAnOggVorbisFileLandWhereAskedAndPlayOnToTheEnd(t *testing.T) {
	b := multiOgg(t)
	frames, err := oggFrames(b, openOggDecoder)
	if err != nil || frames != 64000 {
		t.Fatalf("frames = %d, err = %v", frames, err)
	}
	d, err := newOggDecoder(b, frames, openOggDecoder)
	if err != nil {
		t.Fatal(err)
	}
	ref := make([]float32, 0, frames)
	buf := make([]float32, 3000)
	for {
		n, err := d.Read(buf)
		ref = append(ref, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if int64(len(ref)) != frames {
		t.Fatalf("read %d frames, want %d", len(ref), frames)
	}
	for _, f := range oggSeekSequence {
		at := min(int64(f*float64(frames)), frames)
		if err := d.SetPosition(at); err != nil {
			t.Fatalf("seek to %v: %v", f, err)
		}
		n, err := d.Read(buf[:777])
		if at >= frames {
			if n != 0 || err != io.EOF {
				t.Fatalf("seek to %v: read %d, %v, want the end", f, n, err)
			}
			continue
		}
		if err != nil || n == 0 {
			t.Fatalf("seek to %v: read %d, %v", f, n, err)
		}
		for i := range n {
			if buf[i] != ref[int(at)+i] {
				t.Fatalf("seek to %v: sample %d differs from the linear decode", f, i)
			}
		}
	}
	if err := d.SetPosition(frames / 2); err != nil {
		t.Fatal(err)
	}
	got := 0
	for {
		n, err := d.Read(buf)
		for i := range n {
			if buf[i] != ref[int(frames/2)+got+i] {
				t.Fatalf("tail sample %d differs from the linear decode", got+i)
			}
		}
		got += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if int64(got) != frames-frames/2 {
		t.Fatalf("tail = %d frames, want %d", got, frames-frames/2)
	}
}

const oggPlayerChild = "HAND_OGG_PLAYER_CHILD"

func TestAPlayerOfAnOggVorbisFileSurvivesSeekingAndPlaysToTheEnd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the null output is forced through Linux's sound environment")
	}
	if os.Getenv(oggPlayerChild) == "" {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestAPlayerOfAnOggVorbisFileSurvivesSeekingAndPlaysToTheEnd$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), oggPlayerChild+"=1", "PULSE_SERVER=/nonexistent", "ALSA_CONFIG_PATH=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "--- PASS: "+t.Name()) {
			t.Fatalf("the player run in its own process failed: %v\n%s", err, out)
		}
		return
	}
	p, row := openedAudio(openAudio, multiOgg(t))
	if p == nil {
		t.Fatalf("no player: %q", row)
	}
	defer p.Close()
	dur := p.Duration()
	if dur < 3*time.Second {
		t.Fatalf("duration = %v, want a file of several seconds", dur)
	}
	p.Play()
	for _, f := range oggSeekSequence {
		p.Seek(time.Duration(f * float64(dur)))
		time.Sleep(15 * time.Millisecond)
	}
	p.Play()
	deadline := time.Now().Add(30 * time.Second)
	for p.Playing() {
		if time.Now().After(deadline) {
			t.Fatalf("still playing at %v of %v", p.Position(), dur)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.Position() < dur-time.Second {
		t.Fatalf("stopped at %v of %v", p.Position(), dur)
	}
}

func TestAnyReadBufferStaysOnThePagesTheCheckRead(t *testing.T) {
	b, err := os.ReadFile("testdata/ogg-early-eos.ogg")
	if err != nil {
		t.Fatal(err)
	}
	frames, err := oggFrames(b, openOggDecoder)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 100, 2048, 5000, 16384, 65536} {
		d, err := newOggDecoder(b, frames, openOggDecoder)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]float32, size)
		var got int64
		for {
			n, err := d.Read(buf)
			got += int64(n)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if got != frames {
			t.Fatalf("buffer of %d: read %d frames, want %d", size, got, frames)
		}
	}
}
