package launcher

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
	"unicode/utf16"
)

func fakeLnk(flags uint32, idList, info []byte, strs ...string) []byte {
	b := make([]byte, 76)
	binary.LittleEndian.PutUint32(b, 0x4c)
	binary.LittleEndian.PutUint32(b[20:], flags)
	if idList != nil {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(idList)))
		b = append(b, idList...)
	}
	if info != nil {
		b = binary.LittleEndian.AppendUint32(b, uint32(4+len(info)))
		b = append(b, info...)
	}
	for _, s := range strs {
		u := utf16.Encode([]rune(s))
		b = binary.LittleEndian.AppendUint16(b, uint16(len(u)))
		for _, c := range u {
			b = binary.LittleEndian.AppendUint16(b, c)
		}
	}
	return b
}

func TestReadLnkFindsTheStringsAfterTheTargetData(t *testing.T) {
	b := fakeLnk(0x80|0x1|0x2|0x4|0x8|0x10|0x40, []byte("idlist"), []byte("info!"), "Hand é", `.\x`, `C:\Users\é\bin`, `C:\Users\é\bin\hand.ico`)
	s, ok := readLnk(b)
	if !ok || s.Name != "Hand é" || s.WorkDir != `C:\Users\é\bin` || s.Icon != `C:\Users\é\bin\hand.ico` {
		t.Fatalf("readLnk = %+v, %v", s, ok)
	}
	s, ok = readLnk(fakeLnk(0x80|0x10, nil, nil))
	if ok != false {
		t.Fatalf("a shortcut cut after its header: %+v, %v", s, ok)
	}
	s, ok = readLnk(fakeLnk(0x80|0x10, nil, nil, "dir"))
	if !ok || s.Name != "" || s.WorkDir != "dir" {
		t.Fatalf("no name: %+v, %v", s, ok)
	}
}

func TestReadLnkRefusesWhatIsNotAUnicodeShortcut(t *testing.T) {
	whole := fakeLnk(0x80|0x4, nil, nil, "Hand")
	for name, b := range map[string][]byte{
		"empty":         nil,
		"text":          []byte("[Desktop Entry]\nX-Hand-Managed=true\n" + string(make([]byte, 80))),
		"ansi":          fakeLnk(0x4, nil, nil, "Hand"),
		"cut in a name": whole[:len(whole)-3],
		"cut in a size": whole[:77],
		"info past end": fakeLnk(0x80|0x2|0x4, nil, []byte("x"), "Hand")[:80],
	} {
		if s, ok := readLnk(b); ok {
			t.Errorf("%s: readLnk = %+v, true", name, s)
		}
	}
}

func TestIconIsAnIcoOfTheEmbeddedPNG(t *testing.T) {
	b := iconFile()
	if len(b) < 6 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		t.Fatalf("not an icon: % x", b[:6])
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	var sizes []int
	for i := range n {
		e := b[6+16*i:]
		size, length, offset := int(e[0]), int(binary.LittleEndian.Uint32(e[8:])), int(binary.LittleEndian.Uint32(e[12:]))
		if size == 0 {
			size = 256
		}
		img, err := png.Decode(bytes.NewReader(b[offset : offset+length]))
		if err != nil || img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("entry %d: %v, %v", i, img.Bounds(), err)
		}
		sizes = append(sizes, size)
	}
	if len(sizes) != 4 || sizes[0] != 16 || sizes[3] != 256 {
		t.Fatalf("sizes = %v", sizes)
	}
	if !bytes.Equal(b, iconFile()) {
		t.Fatal("the icon changes between calls")
	}
}
