package board

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func renderText(s string) string {
	var out bytes.Buffer
	w := bufio.NewWriter(&out)
	lw := &lineWriter{w: w, n: 1}
	_, _ = lw.stream(strings.NewReader(s), int64(len(s)))
	_ = w.Flush()
	return out.String()
}

func srcOf(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<pre class="src">(.*?)</pre>`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no source block in %q", out)
	}
	return m[1]
}

func TestTheLineWriterEscapesAndColours(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"html":            {"a<b>&\"'\n", "a&lt;b&gt;&amp;&#34;&#39;\n"},
		"colour":          {"\x1b[32mok\x1b[0m done\n", "<span class=\"x-g\">ok</span> done\n"},
		"bold colour":     {"\x1b[1;31mX\x1b[0m\n", "<span class=\"x-r x-bold\">X</span>\n"},
		"bold":            {"\x1b[1mX\x1b[22mY\n", "<span class=\"x-bold\">X</span>Y\n"},
		"bright":          {"\x1b[92mX\n", "<span class=\"x-g\">X</span>\n"},
		"reset default":   {"\x1b[31mA\x1b[39mB\n", "<span class=\"x-r\">A</span>B\n"},
		"256 colours":     {"\x1b[38;5;31mX\n", "X\n"},
		"256 after red":   {"\x1b[31m\x1b[38;5;32mX\x1b[0m\n", "<span class=\"x-r\">X</span>\n"},
		"true colour":     {"\x1b[38;2;1;2;3mX\n", "X\n"},
		"colon colour":    {"\x1b[38:2::1:2:3mX\n", "X\n"},
		"background":      {"\x1b[41mX\n", "X\n"},
		"huge param":      {"\x1b[99999999999999999999mX\n", "X\n"},
		"other csi":       {"\x1b[2K\x1b[1A\x1b[?25lx\n", "x\n"},
		"osc bel":         {"\x1b]0;title\x07a\n", "a\n"},
		"osc st":          {"\x1b]8;;http://x\x1b\\b\x1b]8;;\x1b\\\n", "b\n"},
		"unterminated":    {"a\x1b]0;never ends\n", "a\n"},
		"other escape":    {"\x1b(Bx\x1bcy\n", "xy\n"},
		"lone escape":     {"a\x1b", "<span class=\"partial\">a</span>"},
		"cr progress":     {"10%\r50%\r100%\n", "100%\n"},
		"cr keeps colour": {"\x1b[31mfoo\rbar\x1b[0m\n", "<span class=\"x-r\">bar</span>\n"},
		"escape and rune": {"a\x1b日b\n", "ab\n"},
		"crlf":            {"a\r\nb\r\r\n", "a\nb\n"},
		"c0":              {"\x01\x07\x08\x0c\x1f\n", `<span class="ctl" title="control character 0x01">␁</span><span class="ctl" title="control character 0x07">␇</span><span class="ctl" title="control character 0x08">␈</span><span class="ctl" title="control character 0x0C">␌</span><span class="ctl" title="control character 0x1F">␟</span>` + "\n"},
		"del and nul":     {"\x7f\x00\n", `<span class="ctl" title="control character 0x7F">␡</span><span class="ctl" title="control character 0x00">␀</span>` + "\n"},
		"tab":             {"a\tb\n", "a\tb\n"},
		"bidi":            {"a\u202eb\u2066c\u2069\u200e\u061c\n", `a<span class="ctl" title="bidirectional control U+202E">U+202E</span>b<span class="ctl" title="bidirectional control U+2066">U+2066</span>c<span class="ctl" title="bidirectional control U+2069">U+2069</span><span class="ctl" title="bidirectional control U+200E">U+200E</span><span class="ctl" title="bidirectional control U+061C">U+061C</span>` + "\n"},
		"c1":              {"a\u0085b\u009bc\n", `a<span class="ctl" title="control character U+0085">U+0085</span>b<span class="ctl" title="control character U+009B">U+009B</span>c` + "\n"},
		"bad utf-8":       {"a\xffb\xc3\n", "a�b�\n"},
		"unicode":         {"héllo ✓ 日本\n", "héllo ✓ 日本\n"},
		"no newline":      {"tail", `<span class="partial">tail</span>`},
		"empty line":      {"a\n\nb\n", "a\n\nb\n"},
	} {
		if got := srcOf(t, renderText(c.in)); got != c.want {
			t.Errorf("%s: %q rendered %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestContainsFoldIgnoresCase(t *testing.T) {
	for _, c := range []struct {
		line, needle string
		want         bool
	}{
		{"First FAILURE here", "first failure", true},
		{"first failure", "first failure", true},
		{"short", "longer needle", false},
		{"", "a", false},
		{"abc", "", true},
		{"ÉCOLE d'été", "école", true},
		{"ÉCOLE", "ecole", false},
		{"x\x1b[31my", "x", true},
		{"AAAB", "aab", true},
		{"AAB", "aaab", false},
	} {
		if got := containsFold([]byte(c.line), []byte(strings.ToLower(c.needle))); got != c.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", c.line, c.needle, got, c.want)
		}
	}
}

func TestColourCarriesToTheNextLine(t *testing.T) {
	got := srcOf(t, renderText("\x1b[31mA\nB\x1b[0m\nC\n"))
	if want := "<span class=\"x-r\">A</span>\n<span class=\"x-r\">B</span>\nC\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestALongLineIsCutWithANote(t *testing.T) {
	long := strings.Repeat("0123456789abcdef", (300<<10)/16)
	out := renderText("first\n" + long + "\nlast\n")
	src := srcOf(t, out)
	if !strings.HasPrefix(src, "first\n0123456789abcdef") || !strings.HasSuffix(src, "\nlast\n") {
		t.Fatalf("lines around the long one are lost: %.80q … %.40q", src, src[len(src)-40:])
	}
	if want := fmt.Sprintf(`<span class="cut">… %s more on this line</span>`+"\n", size(int64(len(long)-maxLine))); !strings.Contains(src, want) {
		t.Errorf("no note %q in the tail %q", want, src[len(src)-120:])
	}
	if n := len(src); n > maxLine+400 {
		t.Errorf("a cut line is %d bytes", n)
	}
	if !strings.Contains(out, `<pre class="gut" aria-hidden="true">1`+"\n2\n3\n</pre>") {
		t.Errorf("the gutter lost a line: %q", out[len(out)-80:])
	}
	huge := renderText(strings.Repeat("x", 3<<20))
	if n := len(huge); n > maxLine+400 || !strings.Contains(huge, "more on this line") {
		t.Errorf("a 3 MiB line with no newline rendered %d bytes", n)
	}
}

func TestControlCharactersCannotBlowUpTheHTML(t *testing.T) {
	line := strings.Repeat("\x01\u202e", 100) + "\n"
	in := strings.Repeat(line, 10000)
	out := renderText(in)
	if n := strings.Count(out, `<span class="ctl"`); n != maxMarks {
		t.Errorf("%d marked controls, want %d", n, maxMarks)
	}
	if limit := 4*len(in) + 2<<20; len(out) > limit {
		t.Errorf("%d bytes of input rendered %d bytes of HTML, over %d", len(in), len(out), limit)
	}
	if !strings.Contains(out, "␁U+202E␁U+202E\n") {
		t.Errorf("controls past the budget are not shown as plain marks")
	}
	checkMarkup(t, in)
}

func TestChunksHoldAtMost500Lines(t *testing.T) {
	var in strings.Builder
	for i := 1; i <= 1001; i++ {
		fmt.Fprintf(&in, "l%d\n", i)
	}
	out := renderText(in.String())
	for _, from := range []string{"1", "501", "1001"} {
		if !strings.Contains(out, `<div class="chunk" data-from="`+from+`">`) {
			t.Errorf("no chunk from %s", from)
		}
	}
	if got := strings.Count(out, `<div class="chunk"`); got != 3 {
		t.Errorf("%d chunks", got)
	}
	if !strings.Contains(out, "\n500\n</pre></div><div class=\"chunk\" data-from=\"501\">") || !strings.HasSuffix(out, "1001\n</pre></div>") {
		t.Errorf("chunk boundaries are wrong:\n%.300s", out)
	}
}

var allowedMarkup = regexp.MustCompile(`</span>|</pre>|</div>|<span class="(x-[kgrybmcw]( x-bold)?|x-bold|ctl|cut|partial)"( title="[A-Za-z0-9+ ]+")?>|<div class="chunk" data-from="\d+">|<pre class="src">|<pre class="gut" aria-hidden="true">`)

func checkMarkup(t *testing.T, in string) {
	t.Helper()
	out := renderText(in)
	if left := allowedMarkup.ReplaceAllString(out, ""); strings.ContainsAny(left, "<>") {
		t.Fatalf("%q rendered markup outside the allowlist: %q", in, left)
	}
	if strings.Count(out, "<span") != strings.Count(out, "</span>") {
		t.Fatalf("%q left a span open: %q", in, out)
	}
}

func FuzzTextEscapes(f *testing.F) {
	for _, s := range []string{
		"<img src=x onerror=alert(1)>", "a<b", "\x1b[31m<script>\x1b[0m", "x\r<y", "&lt;", "\"'", "\u202e<", "\x1b]0;<script>\x07", "\x1b[38;5;31m<b>", "\x1b[1;;31;<m",
		"\x1b[", "\x1b", "\x1b]", "\x1b]8;;", "\xff<", "\x00<\x01>", "a\x1b[31mb\nc\x1b[0md", "\x1b[31m\x1b[1m\n\x1b[0m<", "x\x1b[999999999999999999999m<", "\x1b[38:2::<:2:3m",
	} {
		f.Add(s)
	}
	f.Fuzz(checkMarkup)
}

func TestNoSeedProducesMarkup(t *testing.T) {
	for _, s := range []string{"<script>alert(1)</script>", "\x1b[31m<b>\x1b[0m</b>", "&#60;script&#62;", "\u202e<img>", "\x1b]0;<x>\x07<y>", "\x1b[41;38;2;<mx"} {
		checkMarkup(t, s)
	}
}

type sink struct{ h http.Header }

func (s sink) Header() http.Header       { return s.h }
func (sink) Write(b []byte) (int, error) { return len(b), nil }
func (sink) WriteHeader(int)             {}

func TestAHugeFileIsReadInBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 64 MiB")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "reports", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "big.log"))
	if err != nil {
		t.Fatal(err)
	}
	line := "2026-10-19T02:00:00.000Z \x1b[32m✓\x1b[0m pkg/export/region (0.12s) " + strings.Repeat("x", 40) + "\n"
	block := []byte(strings.Repeat(line, (1<<20)/len(line)))
	for range 64 {
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := New(st, "tok", Options{Home: home}).(*Board)
	for _, q := range []string{"", "?at=0", "?line=700000", "?at=33554432", "?find=region", "?find=nowhere", "?from=0&line=1", "?from=60000000&line=1", "?download"} {
		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		var peak atomic.Uint64
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
			for {
				select {
				case <-stop:
					return
				default:
				}
				metrics.Read(s)
				if v := s[0].Value.Uint64(); v > peak.Load() {
					peak.Store(v)
				}
				time.Sleep(100 * time.Microsecond)
			}
		}()
		r := httptest.NewRequest("GET", "/doc/reports/t1/big.log"+q, nil)
		r.SetPathValue("path", "reports/t1/big.log")
		b.doc(sink{http.Header{}}, r)
		close(stop)
		<-done
		grown := int64(peak.Load()) - int64(base.HeapAlloc)
		t.Logf("%-24q +%.1f MiB", q, float64(grown)/(1<<20))
		if grown > 16<<20 {
			t.Errorf("%q grew the heap by %.1f MiB on a 64 MiB file", q, float64(grown)/(1<<20))
		}
	}
}
