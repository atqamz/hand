package desktop

import (
	"hash/crc32"
	"math/rand"
	"os"
	"testing"
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
