package board

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const pairingLink = "https://laptop.tailnet.example/f00000000e012/pair#K7QM3-XRD2P"

func qrMasked(t *testing.T, text string, mask int) *qrCode {
	t.Helper()
	q, err := qrLayout(text)
	if err != nil {
		t.Fatal(err)
	}
	q.mask(mask)
	q.format(mask)
	return q
}

func qrRows(q *qrCode) []string {
	rows := make([]string, q.size)
	for y := range q.size {
		var b strings.Builder
		for x := range q.size {
			b.WriteByte(map[bool]byte{true: '#', false: '.'}[q.dark[y][x]])
		}
		rows[y] = b.String()
	}
	return rows
}

func TestQRCapacityOfEachVersionAtLevelM(t *testing.T) {
	data := []int{16, 28, 44, 64, 86, 108, 124, 154, 182, 216}
	total := []int{26, 44, 70, 100, 134, 172, 196, 242, 292, 346}
	for v := 1; v <= 10; v++ {
		if qrRaw(v) != total[v-1] || qrData(v) != data[v-1] {
			t.Errorf("version %d: %d codewords with %d for data, want %d and %d", v, qrRaw(v), qrData(v), total[v-1], data[v-1])
		}
		longest := data[v-1] - 2
		if v >= 10 {
			longest--
		}
		for n, want := range map[int]int{longest: v, longest + 1: v + 1} {
			if n > 213 {
				continue
			}
			q, err := qrEncode(strings.Repeat("x", n))
			if err != nil || (q.size-17)/4 != want {
				t.Errorf("%d bytes: version %d (%v), want %d", n, (q.size-17)/4, err, want)
			}
		}
	}
}

func TestQRRefusesWhatVersion10CannotHold(t *testing.T) {
	if _, err := qrEncode(strings.Repeat("x", 213)); err != nil {
		t.Fatalf("213 bytes: %v", err)
	}
	for _, text := range []string{strings.Repeat("x", 214), strings.Repeat("é", 200)} {
		q, err := qrEncode(text)
		if err != errQRTooLong || q != nil {
			t.Fatalf("%d bytes = %v, %v; want errQRTooLong", len(text), q, err)
		}
		if svg, err := qrSVG(text, "label"); err != errQRTooLong || svg != "" {
			t.Fatalf("%d bytes as SVG = %q, %v; want no markup and errQRTooLong", len(text), svg, err)
		}
	}
}

func TestQRFormatBitsMatchTheSpecificationTable(t *testing.T) {
	want := []string{"101010000010010", "101000100100101", "101111001111100", "101101101001011", "100010111111001", "100000011001110", "100111110010111", "100101010100000"}
	for mask, bits := range want {
		q := qrMasked(t, "hand", mask)
		var first, second strings.Builder
		for j := range 15 {
			var x, y int
			switch {
			case j < 6:
				x, y = j, 8
			case j == 6:
				x, y = 7, 8
			case j == 7:
				x, y = 8, 8
			case j == 8:
				x, y = 8, 7
			default:
				x, y = 8, 14-j
			}
			first.WriteByte(map[bool]byte{true: '1', false: '0'}[q.dark[y][x]])
			if j < 7 {
				x, y = 8, q.size-1-j
			} else {
				x, y = q.size-15+j, 8
			}
			second.WriteByte(map[bool]byte{true: '1', false: '0'}[q.dark[y][x]])
		}
		if first.String() != bits || second.String() != bits {
			t.Errorf("mask %d: format bits %s and %s, want %s twice", mask, first.String(), second.String(), bits)
		}
		if !q.dark[q.size-8][8] {
			t.Errorf("mask %d: the dark module is light", mask)
		}
	}
}

func TestQRVersionBitsMatchTheSpecificationTable(t *testing.T) {
	want := map[int]string{7: "000111110010010100", 8: "001000010110111100", 9: "001001101010011001", 10: "001010010011010011"}
	for v, bits := range want {
		q, err := qrLayout(strings.Repeat("x", qrData(v)-4))
		if err != nil || (q.size-17)/4 != v {
			t.Fatalf("version %d: layout %v, size %d", v, err, q.size)
		}
		var topRight, bottomLeft strings.Builder
		for i := 17; i >= 0; i-- {
			topRight.WriteByte(map[bool]byte{true: '1', false: '0'}[q.dark[i/3][q.size-11+i%3]])
			bottomLeft.WriteByte(map[bool]byte{true: '1', false: '0'}[q.dark[q.size-11+i%3][i/3]])
		}
		if topRight.String() != bits || bottomLeft.String() != bits {
			t.Errorf("version %d: %s and %s, want %s twice", v, topRight.String(), bottomLeft.String(), bits)
		}
	}
	q, _ := qrLayout(strings.Repeat("x", qrData(6)-3))
	if (q.size-17)/4 != 6 || q.reserve[0][q.size-11] {
		t.Errorf("version 6 reserves a version information block")
	}
}

func TestQRReedSolomonMatchesPublishedVectors(t *testing.T) {
	for _, c := range []struct{ name, data, ecc string }{
		{"ISO/IEC 18004 annex I, 01234567 at 1-M", "16 32 12 86 97 128 236 17 236 17 236 17 236 17 236 17", "165 36 212 193 237 54 199 135 44 85"},
		{"HELLO WORLD at 1-M", "32 91 11 120 209 114 220 77 67 64 236 17 236 17 236 17", "196 35 39 119 235 215 231 226 93 23"},
	} {
		got := qrRemainder(bytesOf(t, c.data), qrDivisor(10))
		if want := bytesOf(t, c.ecc); !bytes.Equal(got, want) {
			t.Errorf("%s: %v, want %v", c.name, got, want)
		}
	}
	if qrMul(2, 128) != 0x1D || qrMul(0x57, 1) != 0x57 || qrMul(0x57, 0) != 0 || qrMul(0x57, 0x83) != qrMul(0x83, 0x57) {
		t.Error("GF(256) product is wrong")
	}
	if !bytes.Equal(qrDivisor(2), []byte{3, 2}) || !bytes.Equal(qrDivisor(7), bytesOf(t, "127 122 154 164 11 68 117")) {
		t.Errorf("generator polynomials are wrong: %v %v", qrDivisor(2), qrDivisor(7))
	}
}

func bytesOf(t *testing.T, list string) []byte {
	t.Helper()
	var out []byte
	for _, f := range strings.Fields(list) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, byte(n))
	}
	return out
}

func TestQRBlocksHaveZeroSyndromesAndInterleaveInOrder(t *testing.T) {
	for v := 1; v <= 10; v++ {
		data := make([]byte, qrData(v))
		for i := range data {
			data[i] = byte(i*31 + v)
		}
		blocks, ecc, raw := qrBlocks[v], qrECC[v], qrRaw(v)
		out := qrInterleave(v, data)
		if len(out) != raw {
			t.Fatalf("version %d: %d codewords, want %d", v, len(out), raw)
		}
		short := blocks - raw%blocks
		lens := make([]int, blocks)
		for i := range lens {
			lens[i] = raw/blocks - ecc
			if i >= short {
				lens[i]++
			}
		}
		parts := make([][]byte, blocks)
		pos := 0
		for i := 0; i < lens[blocks-1]; i++ {
			for j := range blocks {
				if i < lens[j] {
					parts[j] = append(parts[j], out[pos])
					pos++
				}
			}
		}
		for i := 0; i < ecc; i++ {
			for j := range blocks {
				parts[j] = append(parts[j], out[pos])
				pos++
			}
		}
		k := 0
		for j, p := range parts {
			if !bytes.Equal(p[:lens[j]], data[k:k+lens[j]]) {
				t.Errorf("version %d block %d: data codewords moved", v, j)
			}
			k += lens[j]
			root := byte(1)
			for s := range ecc {
				var acc byte
				for _, c := range p {
					acc = qrMul(acc, root) ^ c
				}
				if acc != 0 {
					t.Errorf("version %d block %d: syndrome %d is %d", v, j, s, acc)
				}
				root = qrMul(root, 2)
			}
		}
	}
}

func TestQRMatchesTheGoldenMatrices(t *testing.T) {
	raw, err := os.ReadFile("testdata/qr.golden")
	if err != nil {
		t.Fatal(err)
	}
	versions := map[int]bool{}
	for _, block := range strings.Split(strings.TrimSpace(string(raw)), "\n\n") {
		text, matrix, _ := strings.Cut(block, "\n")
		q, err := qrEncode(text)
		if err != nil {
			t.Fatal(err)
		}
		versions[(q.size-17)/4] = true
		got, want := qrRows(q), strings.Split(matrix, "\n")
		if len(got) != len(want) {
			t.Fatalf("%d bytes: %d rows, want %d", len(text), len(got), len(want))
		}
		for y := range got {
			if got[y] != want[y] {
				t.Errorf("%d bytes: row %d\n got %s\nwant %s", len(text), y, got[y], want[y])
			}
		}
	}
	if len(versions) != 5 || !versions[1] || !versions[2] || !versions[4] || !versions[7] || !versions[10] {
		t.Fatalf("the golden file covers versions %v, want 1, 2, 4, 7 and 10", versions)
	}
}

func TestQRPenaltyScoresOfEachMask(t *testing.T) {
	for _, c := range []struct {
		text   string
		scores [8]int
	}{
		{"a", [8]int{1246, 1244, 1068, 1122, 1100, 1028, 1133, 1207}},
		{pairingLink, [8]int{1731, 1683, 1427, 1460, 1669, 1637, 1715, 1638}},
		{strings.Repeat("0123456789", 14), [8]int{2721, 3484, 2198, 2504, 2725, 2768, 2620, 2649}},
		{strings.Repeat("x", 213), [8]int{2082, 2566, 2454, 2867, 3725, 2689, 2834, 2919}},
	} {
		best := 0
		for m, want := range c.scores {
			if got := qrMasked(t, c.text, m).penalty(); got != want {
				t.Errorf("%d bytes, mask %d: penalty %d, want %d", len(c.text), m, got, want)
			}
			if want < c.scores[best] {
				best = m
			}
		}
		chosen, _ := qrEncode(c.text)
		if want := qrMasked(t, c.text, best); !bytes.Equal([]byte(strings.Join(qrRows(chosen), "")), []byte(strings.Join(qrRows(want), ""))) {
			t.Errorf("%d bytes: the code does not use mask %d, the lowest penalty", len(c.text), best)
		}
	}
}

func TestQRPenaltyRulesOnSmallGrids(t *testing.T) {
	grid := func(rows ...string) *qrCode {
		q := &qrCode{size: len(rows), dark: make([][]bool, len(rows))}
		for y, r := range rows {
			q.dark[y] = make([]bool, len(r))
			for x, c := range r {
				q.dark[y][x] = c == '#'
			}
		}
		return q
	}
	for _, c := range []struct {
		name string
		q    *qrCode
		want int
	}{
		{"all dark: runs of 5 in 10 lines, 16 squares, 100% dark", grid("#####", "#####", "#####", "#####", "#####"), 10*3 + 16*3 + 90},
		{"a checkerboard at exactly 50% costs nothing", grid("#.#.#.", ".#.#.#", "#.#.#.", ".#.#.#", "#.#.#.", ".#.#.#"), 0},
	} {
		if got := c.q.penalty(); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestQRFunctionPatterns(t *testing.T) {
	for _, text := range []string{"hand", pairingLink, strings.Repeat("x", 213)} {
		q, err := qrEncode(text)
		if err != nil {
			t.Fatal(err)
		}
		n := q.size
		for _, o := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
			for dy := range 7 {
				for dx := range 7 {
					ring := max(qrAbs(dx-3), qrAbs(dy-3))
					if got := q.dark[o[1]+dy][o[0]+dx]; got != (ring != 2) {
						t.Fatalf("%d bytes: finder at %v, module %d,%d is %v", len(text), o, dx, dy, got)
					}
				}
			}
		}
		for i := 8; i < n-8; i++ {
			if q.dark[6][i] != (i%2 == 0) || q.dark[i][6] != (i%2 == 0) {
				t.Fatalf("%d bytes: timing pattern breaks at %d", len(text), i)
			}
		}
		for i := range 8 {
			for _, p := range [][2]int{{7, i}, {i, 7}, {n - 8, i}, {n - 1 - i, 7}, {7, n - 1 - i}, {i, n - 8}} {
				if q.dark[p[1]][p[0]] {
					t.Fatalf("%d bytes: separator module %v is dark", len(text), p)
				}
			}
		}
	}
}

func TestQRSVGMarkup(t *testing.T) {
	svg, err := qrSVG(pairingLink, `QR code for the "pairing" link`)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	if !strings.HasPrefix(s, `<svg class="qr" viewBox="0 0 41 41" role="img" aria-label="QR code for the &#34;pairing&#34; link"`) || !strings.HasSuffix(s, `"/></svg>`) {
		t.Fatalf("unexpected frame: %.160s", s)
	}
	for _, banned := range []string{"style", "<script", "on", "href", "data:", "url(", "var(", "http", "<image", "<style", "<use", "<foreignObject"} {
		if banned == "on" {
			if regexp.MustCompile(`\son[a-z]+=`).MatchString(s) {
				t.Errorf("the SVG has an event handler")
			}
			continue
		}
		if strings.Contains(s, banned) {
			t.Errorf("the SVG holds %q", banned)
		}
	}
	if strings.Count(s, "<path") != 1 || strings.Count(s, "<rect") != 1 || strings.Count(s, "<") != 4 {
		t.Errorf("the SVG is not one rectangle and one path:\n%.300s", s)
	}
	fills := regexp.MustCompile(` fill="([^"]*)"`).FindAllStringSubmatch(s, -1)
	if len(fills) != 2 || fills[0][1] != "#fff" || fills[1][1] != "#000" {
		t.Errorf("fills = %v, want white then black", fills)
	}
	if len(s) > 6000 {
		t.Errorf("%d bytes of SVG for the pairing link", len(s))
	}
}

func TestQRSVGKeepsAQuietZoneOfFourModules(t *testing.T) {
	for _, text := range []string{"hand", pairingLink, strings.Repeat("x", 213)} {
		svg, err := qrSVG(text, "label")
		if err != nil {
			t.Fatal(err)
		}
		img := qrBitmap(t, string(svg), 1)
		q, _ := qrEncode(text)
		if len(img) != q.size+8 {
			t.Fatalf("%d bytes: side %d, want %d", len(text), len(img), q.size+8)
		}
		for y, row := range img {
			for x, dark := range row {
				inside := x >= 4 && y >= 4 && x < q.size+4 && y < q.size+4
				if !inside && dark {
					t.Fatalf("%d bytes: module %d,%d of the quiet zone is dark", len(text), x, y)
				}
				if inside && dark != q.dark[y-4][x-4] {
					t.Fatalf("%d bytes: module %d,%d differs from the matrix", len(text), x, y)
				}
			}
		}
	}
}

var qrPath = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)

func qrBitmap(t *testing.T, svg string, scale int) [][]bool {
	t.Helper()
	var side int
	if _, err := fmt.Sscanf(svg[strings.Index(svg, "viewBox="):], `viewBox="0 0 %d`, &side); err != nil {
		t.Fatal(err)
	}
	img := make([][]bool, side*scale)
	for i := range img {
		img[i] = make([]bool, side*scale)
	}
	d := svg[strings.Index(svg, ` d="`)+4 : strings.LastIndex(svg, `"/>`)]
	covered := 0
	for _, m := range qrPath.FindAllStringSubmatch(d, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		if m[3] != m[4] {
			t.Fatalf("path %q does not return to its start", m[0])
		}
		covered += len(m[0])
		for dy := range scale {
			for dx := range w * scale {
				img[y*scale+dy][x*scale+dx] = true
			}
		}
	}
	if covered != len(d) {
		t.Fatalf("the path holds commands the test does not read: %d of %d bytes", covered, len(d))
	}
	return img
}

func TestQRDecodesWithZbar(t *testing.T) {
	zbar, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("zbarimg is not installed: the round-trip decode of lengths 1 to 213 did not run")
	}
	dir := t.TempDir()
	r := rand.New(rand.NewPCG(1, 2))
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~/:#?=&%+"
	texts := make([]string, 0, 214)
	for n := 1; n <= 213; n++ {
		var b strings.Builder
		for range n {
			b.WriteByte(alphabet[r.IntN(len(alphabet))])
		}
		texts = append(texts, b.String())
	}
	texts = append(texts, pairingLink)
	versions := map[int]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, text := range texts {
		svg, err := qrSVG(text, "label")
		if err != nil {
			t.Fatalf("%d bytes: %v", len(text), err)
		}
		q, _ := qrEncode(text)
		mu.Lock()
		versions[(q.size-17)/4]++
		mu.Unlock()
		const scale = 6
		bits := qrBitmap(t, string(svg), scale)
		pic := image.NewGray(image.Rect(0, 0, len(bits), len(bits)))
		for y, row := range bits {
			for x, dark := range row {
				c := color.Gray{255}
				if dark {
					c = color.Gray{0}
				}
				pic.SetGray(x, y, c)
			}
		}
		file := filepath.Join(dir, fmt.Sprintf("%03d.png", i))
		var buf bytes.Buffer
		if err := png.Encode(&buf, pic); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := exec.Command(zbar, "--raw", "-q", "-Sbinary", file).Output()
			if err != nil {
				t.Errorf("%d bytes: zbarimg found no code: %v", len(text), err)
				return
			}
			if got := strings.TrimSuffix(string(out), "\n"); got != text {
				t.Errorf("%d bytes: decoded %q, want %q", len(text), got, text)
			}
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	t.Logf("zbarimg decoded %d codes from their SVG; codes per version: %v", len(texts), versions)
}

func TestQRCostOfThePairingLinkStaysSmall(t *testing.T) {
	if allocs := testing.AllocsPerRun(20, func() { _, _ = qrSVG(pairingLink, "label") }); allocs > 400 {
		t.Errorf("the pairing link takes %.0f allocations, 94 when written (317 under -race)", allocs)
	}
	worst := strings.Repeat("x", 213)
	if allocs := testing.AllocsPerRun(5, func() { _, _ = qrSVG(worst, "label") }); allocs > 1200 {
		t.Errorf("213 bytes take %.0f allocations, 162 when written (835 under -race)", allocs)
	}
	fastest := time.Hour
	for range 7 {
		start := time.Now()
		_, _ = qrSVG(worst, "label")
		fastest = min(fastest, time.Since(start))
	}
	if fastest > 150*time.Millisecond {
		t.Errorf("the fastest of 7 runs for 213 bytes took %v, 1.8 ms when written, 16 ms under -race", fastest)
	}
}

func BenchmarkQRSVG(b *testing.B) {
	var n int
	for b.Loop() {
		s, _ := qrSVG(pairingLink, "label")
		n = len(s)
	}
	b.ReportMetric(float64(n), "svg-bytes")
}
