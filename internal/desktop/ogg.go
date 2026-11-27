package desktop

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/egoist/mygo/audio"
)

var errAudioDecoder = errors.New("the audio decoder failed")

const (
	oggPageHead = 27
	oggEOS      = 4
	oggMaxRate  = 384000
)

type oggPage struct {
	flags   byte
	granule int64
	serial  uint32
}

func oggPages(b []byte) ([]oggPage, bool) {
	var ps []oggPage
	for off := 0; off < len(b); {
		if len(b)-off < oggPageHead || string(b[off:off+4]) != "OggS" || b[off+4] != 0 {
			return nil, false
		}
		n := int(b[off+26])
		if len(b)-off < oggPageHead+n {
			return nil, false
		}
		size := 0
		for _, s := range b[off+oggPageHead : off+oggPageHead+n] {
			size += int(s)
		}
		end := off + oggPageHead + n + size
		if end > len(b) {
			return nil, false
		}
		ps = append(ps, oggPage{b[off+5], int64(binary.LittleEndian.Uint64(b[off+6:])), binary.LittleEndian.Uint32(b[off+14:])})
		off = end
	}
	return ps, len(ps) > 0
}

// The decoder is only ever read from the start to the first end of stream, which is what this accepts: one stream, an end flag on the last page and no other, and granules that never fall or pass the frames decoded.
func oggStructure(ps []oggPage, frames int64) bool {
	prev := int64(0)
	for i, p := range ps {
		if p.serial != ps[0].serial || (p.flags&oggEOS != 0) != (i == len(ps)-1) {
			return false
		}
		if p.granule == -1 {
			continue
		}
		if p.granule < prev {
			return false
		}
		prev = p.granule
	}
	return prev <= frames
}

func oggPackets(b []byte, want int) [][]byte {
	var out [][]byte
	var cur []byte
	for off := 0; off < len(b) && len(out) < want; {
		n := int(b[off+26])
		body := off + oggPageHead + n
		for _, lace := range b[off+oggPageHead : body] {
			cur = append(cur, b[body:body+int(lace)]...)
			body += int(lace)
			if lace < 255 {
				out = append(out, cur)
				cur = nil
				if len(out) == want {
					break
				}
			}
		}
		off = body
	}
	return out
}

// The Vorbis decoder allocates what the comment header declares before it checks it, and MyGo sizes a ring from the sample rate; both are refused here before either runs.
func oggHeaders(b []byte) bool {
	ps := oggPackets(b, 2)
	if len(ps) < 2 {
		return false
	}
	id, com := ps[0], ps[1]
	if len(id) < 16 || string(id[:7]) != "\x01vorbis" {
		return false
	}
	if rate := binary.LittleEndian.Uint32(id[12:]); rate < 1 || rate > oggMaxRate {
		return false
	}
	if len(com) < 8 || string(com[:7]) != "\x03vorbis" {
		return false
	}
	com = com[7:]
	vendor := int64(binary.LittleEndian.Uint32(com))
	if int64(len(com)) < 8+vendor {
		return false
	}
	com = com[4+vendor:]
	count := int64(binary.LittleEndian.Uint32(com))
	return count <= int64(len(com)-4)/4
}

func oggFrames(b []byte, open func([]byte) (audio.Decoder, error)) (frames int64, err error) {
	defer func() {
		if recover() != nil {
			frames, err = 0, errAudioDecoder
		}
	}()
	d, err := open(b)
	if err != nil {
		return 0, err
	}
	ch := d.Channels()
	if ch < 1 || ch > 255 {
		return 0, errAudioDecoder
	}
	buf := make([]float32, oggChunk*ch)
	for {
		n, err := d.Read(buf)
		frames += int64(n / ch)
		if err == io.EOF {
			return frames, nil
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, errAudioDecoder
		}
	}
}

func openOgg(b []byte) (*audio.Player, error) {
	return openOggWith(b, openOggDecoder)
}

func openOggWith(b []byte, open func([]byte) (audio.Decoder, error)) (*audio.Player, error) {
	ps, ok := oggPages(b)
	if !ok {
		return nil, errAudioDecoder
	}
	if !oggHeaders(b) {
		return nil, errAudioDecoder
	}
	frames, err := oggFrames(b, open)
	if err != nil {
		return nil, err
	}
	if !oggStructure(ps, frames) {
		return nil, errAudioDecoder
	}
	d, err := newOggDecoder(b, frames, open)
	if err != nil {
		return nil, err
	}
	return audio.NewPlayer(d), nil
}

// MyGo calls SetPosition on a goroutine of its own that this code cannot recover, and the Ogg Vorbis decoder's seek reaches pages the linear decode never read; its Read also runs on to later pages when handed a larger buffer than the check used. A seek here re-decodes from the start, and every read of the decoder is oggChunk frames, as in the check, so a player only takes the path the check ran.
type oggDecoder struct {
	data   []byte
	open   func([]byte) (audio.Decoder, error)
	frames int64
	cur    audio.Decoder
	rate   int
	ch     int
	pos    int64
	chunk  []float32
	have   []float32
}

const oggChunk = 2048

func openOggDecoder(b []byte) (audio.Decoder, error) {
	d, _, err := audio.NewDecoder(bytes.NewReader(b))
	return d, err
}

func newOggDecoder(b []byte, frames int64, open func([]byte) (audio.Decoder, error)) (*oggDecoder, error) {
	d := &oggDecoder{data: b, open: open, frames: frames}
	if err := d.restart(); err != nil {
		return nil, err
	}
	d.rate, d.ch = d.cur.SampleRate(), d.cur.Channels()
	d.chunk = make([]float32, oggChunk*d.ch)
	return d, nil
}

func (d *oggDecoder) restart() error {
	c, err := d.open(d.data)
	if err != nil {
		return err
	}
	d.cur, d.pos, d.have = c, 0, nil
	return nil
}

func (d *oggDecoder) SampleRate() int { return d.rate }
func (d *oggDecoder) Channels() int   { return d.ch }
func (d *oggDecoder) Len() int64      { return d.frames }

func (d *oggDecoder) fill() error {
	if d.cur == nil {
		return io.EOF
	}
	n, err := d.cur.Read(d.chunk)
	d.have = d.chunk[:n-n%d.ch]
	if err == io.EOF {
		d.cur = nil
		if len(d.have) > 0 {
			return nil
		}
	}
	if err == nil && len(d.have) == 0 {
		return errAudioDecoder
	}
	return err
}

func (d *oggDecoder) Read(buf []float32) (int, error) {
	if len(d.have) == 0 {
		if err := d.fill(); err != nil {
			return 0, err
		}
	}
	n := copy(buf[:len(buf)-len(buf)%d.ch], d.have)
	d.have = d.have[n:]
	d.pos += int64(n / d.ch)
	return n, nil
}

func (d *oggDecoder) SetPosition(frame int64) error {
	frame = max(frame, 0)
	if frame >= d.frames {
		d.cur, d.pos, d.have = nil, d.frames, nil
		return nil
	}
	if d.cur == nil || frame < d.pos {
		if err := d.restart(); err != nil {
			return err
		}
	}
	for d.pos < frame {
		if len(d.have) == 0 {
			if err := d.fill(); err == io.EOF {
				return nil
			} else if err != nil {
				return err
			}
		}
		n := min(int64(len(d.have)/d.ch), frame-d.pos)
		d.have = d.have[n*int64(d.ch):]
		d.pos += n
	}
	return nil
}
