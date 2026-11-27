package desktop

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/audio"
)

var oggSpyOnce sync.Once
var oggSpyOpened atomic.Int32

func spyOnOgg() {
	oggSpyOnce.Do(func() {
		audio.RegisterFormat("spy", "OggS", func(io.ReadSeeker) (audio.Decoder, error) {
			oggSpyOpened.Add(1)
			return nil, errors.New("a decoder was opened for an OggS stream")
		})
	})
}

func oggCRC(b []byte) uint32 {
	var crc uint32
	for _, c := range b {
		crc ^= uint32(c) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func oggPage(seq uint32, flags byte, packet []byte) []byte {
	var lace []byte
	for n := len(packet); ; n -= 255 {
		if n >= 255 {
			lace = append(lace, 255)
			continue
		}
		lace = append(lace, byte(n))
		break
	}
	p := append([]byte("OggS\x00"), flags)
	p = binary.LittleEndian.AppendUint64(p, 0)
	p = binary.LittleEndian.AppendUint32(p, 1)
	p = binary.LittleEndian.AppendUint32(p, seq)
	p = append(p, 0, 0, 0, 0, byte(len(lace)))
	p = append(p, lace...)
	p = append(p, packet...)
	binary.LittleEndian.PutUint32(p[22:], oggCRC(p))
	return p
}

type bitWriter struct {
	b    []byte
	bits int
}

func (w *bitWriter) put(v uint64, n int) {
	for i := range n {
		if w.bits%8 == 0 {
			w.b = append(w.b, 0)
		}
		w.b[len(w.b)-1] |= byte(v>>i&1) << (w.bits % 8)
		w.bits++
	}
}

func hostileVorbis(entries, dims int, lookup int) []byte {
	ident := append([]byte("\x01vorbis"), make([]byte, 23)...)
	binary.LittleEndian.PutUint32(ident[11:], 2)
	binary.LittleEndian.PutUint32(ident[15:], 44100)
	ident = append(ident, 0xb8, 1)
	comment := append([]byte("\x03vorbis"), make([]byte, 8)...)
	comment = append(comment, 1)
	setup := &bitWriter{b: []byte("\x05vorbis"), bits: 7 * 8}
	setup.put(0, 8)
	setup.put(0x564342, 24)
	setup.put(uint64(dims), 16)
	setup.put(uint64(entries), 24)
	setup.put(0, 1)
	setup.put(0, 1)
	for range entries {
		setup.put(4, 5)
	}
	setup.put(uint64(lookup), 4)
	if lookup != 0 {
		setup.put(0, 32)
		setup.put(0, 32)
		setup.put(15, 4)
		setup.put(0, 1)
	}
	return append(append(oggPage(0, 2, ident), oggPage(1, 0, comment)...), oggPage(2, 0, setup.b)...)
}

func TestAnOggFileIsRefusedAndNoDecoderIsOpened(t *testing.T) {
	spyOnOgg()
	for name, b := range map[string][]byte{
		"lookup type 2 with a huge entries times dimensions": hostileVorbis(4000, 65535, 2),
		"lookup type 1 with a huge dimension count":          hostileVorbis(4000, 65535, 1),
		"no entries":             hostileVorbis(0, 1, 0),
		"a bare capture pattern": []byte("OggS"),
		"an Opus stream":         []byte("OggS\x00\x02OpusHead"),
	} {
		t.Run(name, func(t *testing.T) {
			before := oggSpyOpened.Load()
			p, err := openAudio(b)
			if p != nil || !errors.Is(err, audio.ErrFormat) {
				t.Fatalf("openAudio = %v, %v, want no player and ErrFormat", p, err)
			}
			p, row := openedAudio(openAudio, b)
			if p != nil || row != audioFormat {
				t.Fatalf("openedAudio = %v, %q, want no player and %q", p, row, audioFormat)
			}
			if n := oggSpyOpened.Load(); n != before {
				t.Fatalf("a decoder was opened %d times for an Ogg file", n-before)
			}
		})
	}
}

type rated int

func (r rated) SampleRate() int           { return int(r) }
func (rated) Channels() int               { return 1 }
func (rated) Read([]float32) (int, error) { return 0, io.EOF }
func (rated) SetPosition(int64) error     { return nil }
func (rated) Len() int64                  { return 0 }

func TestADecoderThatReportsNoSampleRateIsRefused(t *testing.T) {
	for magic, rate := range map[string]int{"ZERO": 0, "NEG-": -1} {
		audio.RegisterFormat("rate"+magic, magic, func(io.ReadSeeker) (audio.Decoder, error) { return rated(rate), nil })
		p, err := openAudio([]byte(magic))
		if p != nil || !errors.Is(err, errAudioRate) {
			t.Errorf("rate %d: %v, %v, want no player and errAudioRate", rate, p, err)
		}
	}
}

func wavBytes(rate uint32) []byte { return wavChannelBytes(rate, 1) }

func wavChannelBytes(rate uint32, channels uint16) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+4))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, channels)
	binary.Write(&b, binary.LittleEndian, rate)
	binary.Write(&b, binary.LittleEndian, rate*uint32(channels)*2)
	binary.Write(&b, binary.LittleEndian, channels*2)
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(4))
	b.Write([]byte{0, 0, 0, 0})
	return b.Bytes()
}

func TestAWavFileOpensOnlyBetweenOneAndThreeEightyFourThousandHertz(t *testing.T) {
	for _, tc := range []struct {
		rate uint32
		ok   bool
	}{
		{0, false}, {1, true}, {8000, true}, {384000, true}, {384001, false}, {4294967295, false},
	} {
		p, err := openAudio(wavBytes(tc.rate))
		if p != nil {
			p.Close()
		}
		if tc.ok && (p == nil || err != nil) {
			t.Errorf("rate %d: %v, %v, want a player", tc.rate, p, err)
		}
		if !tc.ok && (p != nil || err == nil) {
			t.Errorf("rate %d: %v, %v, want a refusal", tc.rate, p, err)
		}
	}
}

func TestAWavFileOverTheRateBoundGetsTheNoticeAndNoPlayer(t *testing.T) {
	p, row := openedAudio(openAudio, wavBytes(audioMaxRate+1))
	if p != nil || row != audioFormat {
		t.Fatalf("openedAudio = %v, %q, want no player and %q", p, row, audioFormat)
	}
}

func TestAWavFileOpensOnlyWithOneToEightChannels(t *testing.T) {
	for _, tc := range []struct {
		channels uint16
		ok       bool
	}{
		{0, false}, {1, true}, {2, true}, {8, true}, {9, false}, {1000, false}, {65535, false},
	} {
		p, err := openAudio(wavChannelBytes(8000, tc.channels))
		if p != nil {
			p.Close()
		}
		if tc.ok && (p == nil || err != nil) {
			t.Errorf("channels %d: %v, %v, want a player", tc.channels, p, err)
		}
		if !tc.ok && (p != nil || err == nil) {
			t.Errorf("channels %d: %v, %v, want a refusal", tc.channels, p, err)
		}
	}
}

func TestAWavFileWithTooManyChannelsGetsTheNoticeAndNoPlayer(t *testing.T) {
	for _, c := range []uint16{9, 1000, 65535} {
		p, row := openedAudio(openAudio, wavChannelBytes(8000, c))
		if p != nil || row != audioFormat {
			t.Fatalf("channels %d: openedAudio = %v, %q, want no player and %q", c, p, row, audioFormat)
		}
	}
}

type channeled int

func (c channeled) SampleRate() int           { return 8000 }
func (c channeled) Channels() int             { return int(c) }
func (channeled) Read([]float32) (int, error) { return 0, io.EOF }
func (channeled) SetPosition(int64) error     { return nil }
func (channeled) Len() int64                  { return 0 }

func TestADecoderThatReportsNoChannelsOrTooManyIsRefused(t *testing.T) {
	for magic, ch := range map[string]int{"CZER": 0, "CNEG": -1, "CNIN": 9} {
		audio.RegisterFormat("chan"+magic, magic, func(io.ReadSeeker) (audio.Decoder, error) { return channeled(ch), nil })
		p, err := openAudio([]byte(magic))
		if p != nil || !errors.Is(err, errAudioChannels) {
			t.Errorf("channels %d: %v, %v, want no player and errAudioChannels", ch, p, err)
		}
	}
}
