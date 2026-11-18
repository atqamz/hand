package launcher

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"sync"
	"unicode/utf16"

	"github.com/atqamz/hand/internal/brand"
)

type lnkStrings struct{ Name, WorkDir, Icon string }

func readLnk(b []byte) (s lnkStrings, ok bool) {
	const header, hasIDList, hasInfo, unicode = 76, 0x1, 0x2, 0x80
	if len(b) < header || binary.LittleEndian.Uint32(b) != 0x4c {
		return s, false
	}
	flags := binary.LittleEndian.Uint32(b[20:])
	if flags&unicode == 0 {
		return s, false
	}
	p := header
	if flags&hasIDList != 0 {
		if p+2 > len(b) {
			return s, false
		}
		p += 2 + int(binary.LittleEndian.Uint16(b[p:]))
	}
	if flags&hasInfo != 0 {
		if p+4 > len(b) {
			return s, false
		}
		p += int(binary.LittleEndian.Uint32(b[p:]))
	}
	for _, f := range []struct {
		bit uint32
		to  *string
	}{{0x4, &s.Name}, {0x8, new(string)}, {0x10, &s.WorkDir}, {0x20, new(string)}, {0x40, &s.Icon}} {
		if flags&f.bit == 0 {
			continue
		}
		if p < 0 || p+2 > len(b) {
			return s, false
		}
		n := 2 * int(binary.LittleEndian.Uint16(b[p:]))
		p += 2
		if p+n > len(b) {
			return s, false
		}
		u := make([]uint16, n/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[p+2*i:])
		}
		*f.to = string(utf16.Decode(u))
		p += n
	}
	return s, true
}

var iconFile = sync.OnceValue(func() []byte {
	src, err := png.Decode(bytes.NewReader(brand.Icon512))
	if err != nil {
		panic("launcher: the embedded icon is not a PNG: " + err.Error())
	}
	sizes := []int{16, 32, 64, 256}
	var images [][]byte
	for _, n := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, shrink(src, n)); err != nil {
			panic(err)
		}
		images = append(images, buf.Bytes())
	}
	out := binary.LittleEndian.AppendUint16(nil, 0)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := byte(n)
		if n == 256 {
			dim = 0
		}
		out = append(out, dim, dim, 0, 0)
		out = binary.LittleEndian.AppendUint16(out, 1)
		out = binary.LittleEndian.AppendUint16(out, 32)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(images[i])))
		out = binary.LittleEndian.AppendUint32(out, uint32(offset))
		offset += len(images[i])
	}
	for _, img := range images {
		out = append(out, img...)
	}
	return out
})

func shrink(src image.Image, n int) image.Image {
	box := src.Bounds()
	k := box.Dx() / n
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			var r, g, b, a uint32
			for dy := range k {
				for dx := range k {
					pr, pg, pb, pa := src.At(box.Min.X+x*k+dx, box.Min.Y+y*k+dy).RGBA()
					r, g, b, a = r+pr, g+pg, b+pb, a+pa
				}
			}
			c := uint32(k * k)
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = byte(r/c>>8), byte(g/c>>8), byte(b/c>>8), byte(a/c>>8)
		}
	}
	return dst
}
