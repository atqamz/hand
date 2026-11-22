package inbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type countingReader struct{ read int }

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= 64<<20 {
		return 0, io.EOF
	}
	n := min(len(p), 64<<20-c.read)
	c.read += n
	return n, nil
}

func pngOf(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, w), h)
	ihdr = append(ihdr, 8, 0, 0, 0, 0)
	b := []byte("\x89PNG\r\n\x1a\n")
	b = binary.BigEndian.AppendUint32(b, uint32(len(ihdr)))
	b = append(b, "IHDR"...)
	b = append(b, ihdr...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
}

func jpegOf(w, h uint16) []byte {
	b := []byte{0xff, 0xd8, 0xff, 0xc0, 0, 11, 8}
	b = binary.BigEndian.AppendUint16(b, h)
	b = binary.BigEndian.AppendUint16(b, w)
	return append(b, 1, 1, 0x11, 0, 0xff, 0xda, 0, 8, 1, 1, 0, 0, 0x3f, 0, 0xff, 0xd9)
}

func gifOf(w, h uint16) []byte {
	b := []byte("GIF89a")
	b = binary.LittleEndian.AppendUint16(b, w)
	b = binary.LittleEndian.AppendUint16(b, h)
	return append(b, 0, 0, 0, 0x3b)
}

func riff(chunk string, body []byte) []byte {
	b := append([]byte("RIFF\x00\x00\x00\x00WEBP"), chunk...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(body)))
	return append(b, body...)
}

func webpLossy(w, h uint16) []byte {
	body := []byte{0x10, 0, 0, 0x9d, 0x01, 0x2a}
	body = binary.LittleEndian.AppendUint16(body, w|0xc000)
	body = binary.LittleEndian.AppendUint16(body, h|0x4000)
	return riff("VP8 ", append(body, make([]byte, 4)...))
}

func webpLossless(w, h uint32) []byte {
	body := []byte{0x2f}
	body = binary.LittleEndian.AppendUint32(body, (w-1)|(h-1)<<14)
	return riff("VP8L", append(body, make([]byte, 4)...))
}

func webpExtended(w, h uint32) []byte {
	body := []byte{0, 0, 0, 0, byte(w - 1), byte((w - 1) >> 8), byte((w - 1) >> 16), byte(h - 1), byte((h - 1) >> 8), byte((h - 1) >> 16)}
	return riff("VP8X", body)
}

func TestSaveStopsReadingAtTheFileCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	src := &countingReader{}
	if _, err := Save(dir, now, "big.bin", src); !errors.Is(err, ErrTooLarge) || !TooBig(err) {
		t.Fatalf("a 64 MiB body = %v, want ErrTooLarge", err)
	}
	if src.read > MaxImage+1<<20 {
		t.Fatalf("Save read %d bytes of a body over the %d byte cap", src.read, MaxImage)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused file left %d entries", len(entries))
	}
}

func TestThePixelCapAdmitsTheLimitAndRefusesOnePixelRowMore(t *testing.T) {
	cases := []struct {
		name         string
		ok, tooMany  []byte
		wantPixelsOK int64
	}{
		{"png", pngOf(6000, 6000), pngOf(6000, 6001), 36e6},
		{"jpeg", jpegOf(6000, 6000), jpegOf(6001, 6000), 36e6},
		{"gif", gifOf(6000, 6000), gifOf(6000, 6001), 36e6},
		{"webp lossy", webpLossy(6000, 6000), webpLossy(6000, 6001), 36e6},
		{"webp lossless", webpLossless(6000, 6000), webpLossless(6000, 6001), 36e6},
		{"webp extended", webpExtended(6000, 6000), webpExtended(6000, 6001), 36e6},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if px, known := Pixels(c.ok); !known || px != c.wantPixelsOK {
			t.Errorf("%s: Pixels = %d, %v, want %d", c.name, px, known, c.wantPixelsOK)
		}
		if name, err := Save(dir, now, "a", bytes.NewReader(c.ok)); err != nil || !IsImage(name) {
			t.Errorf("%s at the cap = %q, %v", c.name, name, err)
		}
		if _, err := Save(dir, now, "a.png", bytes.NewReader(c.tooMany)); !errors.Is(err, ErrPixels) || !TooBig(err) {
			t.Errorf("%s over the cap = %v, want ErrPixels", c.name, err)
		}
		if des, _ := os.ReadDir(dir); len(des) != 1 {
			t.Errorf("%s: a refused image left %d entries, want the one saved", c.name, len(des))
		}
	}
}

func TestAnImageWhoseSizeCannotBeReadIsRefused(t *testing.T) {
	for name, data := range map[string][]byte{
		"png":            pngOf(1, 1)[:20],
		"png 2^30":       pngOf(1<<30, 1<<30),
		"png 2^31-1":     pngOf(1<<31-1, 1<<31-1),
		"webp":           []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		"webp-bad-start": append(webpLossy(9, 9)[:23], make([]byte, 12)...),
	} {
		if _, known := Pixels(data); known {
			t.Errorf("%s: a header that cannot be read gave a size", name)
		}
		dir := t.TempDir()
		if got, err := Save(dir, now, "x.png", bytes.NewReader(data)); !errors.Is(err, ErrUnsized) || !TooBig(err) {
			t.Errorf("%s: %q, %v, want ErrUnsized", name, got, err)
		}
		if des, _ := os.ReadDir(dir); len(des) != 0 {
			t.Errorf("%s: a refused image left %d entries", name, len(des))
		}
	}
	if got, err := Save(t.TempDir(), now, "notes.png", strings.NewReader("plain text")); err != nil || !strings.HasSuffix(got, ".png.bin") {
		t.Errorf("text named like an image = %q, %v", got, err)
	}
}

func TestSweepUploadsReportsAFileItCannotRemove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only folder does not stop removal here")
	}
	dir := t.TempDir()
	put(t, filepath.Join(dir, ".upload-old"), 2*time.Hour)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	got, failed := SweepUploads(dir, now, false)
	if got != nil || len(failed) != 1 || failed[0].Name != ".upload-old" || failed[0].Err == nil {
		t.Fatalf("swept %+v, failed %+v", got, failed)
	}
}

func TestSafeNameKeepsLettersMarksAndDigitsOfAnyScript(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":               "report.pdf",
		"../../etc/passwd":         "passwd",
		`..\..\win.ini`:            "win.ini",
		`C:\Users\me\notes.txt`:    "notes.txt",
		".env":                     "env",
		"--rf":                     "rf",
		"..":                       "file",
		"":                         "file",
		"Q3 export (final).csv":    "Q3_export_final_.csv",
		"résumé.pdf":               "résumé.pdf",
		"re\u0301sume\u0301.pdf":   "re\u0301sume\u0301.pdf",
		"日本.txt":                   "日本.txt",
		"नमस्ते.txt":               "नमस्ते.txt",
		"𠀀.txt":                    "𠀀.txt",
		"🙂.txt":                    "_.txt",
		"a\u202eb.txt":             "a_b.txt",
		"a\u200db.txt":             "a_b.txt",
		"a\u034f\u3164\ufe0fb.txt": "a_b.txt",
		"\u3164\u034f.txt":         "_.txt",
		"a\x00b\n.txt":             "a_b_.txt",
		"a\xffb.txt":               "a_b.txt",
		"trailing.":                "trailing",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestALongNameKeepsItsExtensionAndNeverCutsARune(t *testing.T) {
	long := ClipName(strings.Repeat("a", 200) + ".pdf")
	if len(long) != MaxName || !strings.HasSuffix(long, "a.pdf") {
		t.Fatalf("long = %q (%d)", long, len(long))
	}
	odd := ClipName("a." + strings.Repeat("b", 200))
	if len(odd) != MaxName {
		t.Fatalf("odd = %q (%d)", odd, len(odd))
	}
	if dot := ClipName(strings.Repeat("c", 79) + "." + strings.Repeat("d", 30)); dot != strings.Repeat("c", 79) {
		t.Fatalf("a clip that ends on a dot = %q", dot)
	}
	for _, name := range []string{strings.Repeat("é", 100) + ".pdf", strings.Repeat("日", 100), "a" + strings.Repeat("𠀀", 40) + ".txt", strings.Repeat("é", 100)} {
		got := ClipName(name)
		if len(got) > MaxName || !utf8.ValidString(got) || (strings.HasSuffix(name, ".pdf") || strings.HasSuffix(name, ".txt")) && filepathExt(got) != filepathExt(name) {
			t.Errorf("ClipName(%.12q...) = %q (%d bytes)", name, got, len(got))
		}
	}
}

func filepathExt(s string) string { return s[strings.LastIndex(s, "."):] }

func TestASavedNameIsInTheGrammarAndReadsBackByteForByte(t *testing.T) {
	dir := t.TempDir()
	nfc, nfd := "caf\u00e9.txt", "cafe\u0301.txt"
	for _, original := range []string{nfc, nfd, "日本語のメモ.txt", strings.Repeat("é", 100) + ".pdf", "नमस्ते.txt", "𠀀𠀀.txt"} {
		name, err := Save(dir, now, original, strings.NewReader("data"))
		if err != nil {
			t.Fatal(err)
		}
		if !Stored.MatchString(name) || !Name.MatchString(name) || len(Label(name)) > MaxName {
			t.Errorf("Save(%q) = %q, not in the grammar", original, name)
		}
		des, _ := os.ReadDir(dir)
		if !slices.ContainsFunc(des, func(de os.DirEntry) bool { return de.Name() == name }) {
			t.Errorf("Save(%q) = %q, but the folder lists %v", original, name, des)
		}
	}
	a, errA := os.Lstat(filepath.Join(dir, "20261019-120000-1-"+nfc))
	b, errB := os.Lstat(filepath.Join(dir, "20261019-120000-1-"+nfd))
	t.Logf("NFC lookup: %v; NFD lookup: %v; same file when both resolve: %v", errA, errB, errA == nil && errB == nil && os.SameFile(a, b))
}

func TestSaveClipsToEightyBytesOfAnyScript(t *testing.T) {
	name, err := Save(t.TempDir(), now, strings.Repeat("日", 100)+".txt", strings.NewReader("x"))
	if err != nil || !Stored.MatchString(name) || len(Label(name)) > MaxName {
		t.Fatalf("name = %q (%d), %v", name, len(Label(name)), err)
	}
}

func TestSweepUploadsRemovesOnlyStaleUploads(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, ".upload-old"), time.Hour+time.Second)
	put(t, filepath.Join(dir, ".upload-edge"), time.Hour)
	put(t, filepath.Join(dir, ".upload-new"), 59*time.Minute)
	put(t, filepath.Join(dir, "20260101-000000-1.png"), 90*day)
	put(t, filepath.Join(dir, "notes.txt"), 90*day)
	put(t, filepath.Join(dir, ".upload-dir", "20260101-000000-1.png"), 90*day)
	if err := os.Chtimes(filepath.Join(dir, ".upload-dir"), now.Add(-90*day), now.Add(-90*day)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("notes.txt", filepath.Join(dir, ".upload-link")); err != nil {
		t.Fatal(err)
	}
	before := tree(t, dir)
	got, failed := SweepUploads(dir, now, true)
	if failed != nil || !slices.Equal(names(got), []string{".upload-old"}) || got[0].Size != 5 || !mapsEqual(before, tree(t, dir)) {
		t.Fatalf("dry sweep = %+v, folder changed: %v", got, !mapsEqual(before, tree(t, dir)))
	}
	if got, failed := SweepUploads(dir, now, false); failed != nil || !slices.Equal(names(got), []string{".upload-old"}) {
		t.Fatalf("sweep = %+v", got)
	}
	after := tree(t, dir)
	delete(before, ".upload-old")
	delete(before, ".")
	delete(after, ".")
	if !mapsEqual(before, after) {
		t.Fatalf("sweep left %v, want %v", after, before)
	}
	if got, failed := SweepUploads(filepath.Join(dir, "missing"), now, false); got != nil || failed != nil {
		t.Fatalf("sweep of no folder = %+v", got)
	}
}

func TestSaveSweepsAStaleUploadButNotOneInProgress(t *testing.T) {
	dir := t.TempDir()
	put(t, filepath.Join(dir, ".upload-old"), 2*time.Hour)
	put(t, filepath.Join(dir, ".upload-busy"), 10*time.Minute)
	name, err := Save(dir, now, "a.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	des, _ := os.ReadDir(dir)
	var got []string
	for _, de := range des {
		got = append(got, de.Name())
	}
	if want := []string{".upload-busy", name}; !slices.Equal(got, want) {
		t.Fatalf("folder = %v, want %v", got, want)
	}
}

func TestPruneSweepsStaleUploadsAndASweepDryRunKeepsThem(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, ".upload-old"), 2*time.Hour)
	put(t, filepath.Join(dir, ".upload-busy"), time.Minute)
	p, err := Prune(home, now, 30*day, true, nil)
	if err != nil || !slices.Equal(names(p.Swept), []string{".upload-old"}) {
		t.Fatalf("dry prune = %+v, %v", p, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".upload-old")); err != nil {
		t.Fatalf("a dry run removed the upload: %v", err)
	}
	p, err = Prune(home, now, 30*day, false, nil)
	if err != nil || !slices.Equal(names(p.Swept), []string{".upload-old"}) {
		t.Fatalf("prune = %+v, %v", p, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".upload-old")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale upload still there: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".upload-busy")); err != nil {
		t.Fatalf("an upload in progress was swept: %v", err)
	}
}

func TestSaveNeverReplacesAFileOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	got := make([]string, 40)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = Save(dir, now, "same.txt", strings.NewReader(strings.Repeat("x", i+1)))
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, name := range got {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if name == "" || seen[name] || err != nil || len(b) != i+1 {
			t.Fatalf("save %d = %q (%d bytes), %v", i, name, len(b), err)
		}
		seen[name] = true
	}
	des, _ := os.ReadDir(dir)
	if len(des) != len(got) {
		t.Fatalf("%d entries for %d saves", len(des), len(got))
	}
}
