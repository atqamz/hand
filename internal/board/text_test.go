package board_test

import (
	"bufio"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const logLine = "%08d 2026-10-21T01:00:00Z export worker heartbeat ok\n"

var lineLen = int64(len(fmt.Sprintf(logLine, 1)))

func writeLog(t *testing.T, p string, mib int, plant map[int]string) int {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	n := 0
	for written := int64(0); written < int64(mib)<<20; written += lineLen {
		n++
		if extra, ok := plant[n]; ok {
			fmt.Fprintf(w, "%08d %-*s\n", n, int(lineLen)-10, extra)
			continue
		}
		fmt.Fprintf(w, logLine, n)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return n
}

var (
	chunkOpen  = regexp.MustCompile(`<div class="chunk" data-line="(\d+)"( data-partial)?>`)
	endMarker  = regexp.MustCompile(`<span id="text-end" class="text-end" data-next="(\d+)" data-line="(\d+)"></span>`)
	windowText = regexp.MustCompile(`Lines (\d+)–(\d+)\. The Board shows 4 MiB of a large file at a time\.`)
)

func marker(t *testing.T, what, body string) (next int64, line int) {
	t.Helper()
	m := endMarker.FindAllStringSubmatch(body, -1)
	if len(m) != 1 {
		t.Fatalf("%s has %d end markers", what, len(m))
	}
	next, _ = strconv.ParseInt(m[0][1], 10, 64)
	line, _ = strconv.Atoi(m[0][2])
	return next, line
}

func href(body, label string) string {
	m := regexp.MustCompile(`<a href="([^"]+)"(?: rel="(?:prev|next)")?>` + label + `</a>`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

func TestAFiveMiBLogIsShownWholeInChunks(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/notes.txt": "n\n"})
	n := writeLog(t, filepath.Join(home, "reports", "t1", "build.log"), 5, nil)
	start := time.Now()
	body := get(t, h, "/doc/reports/t1/build.log")
	t.Logf("5 MiB page: %d lines, %.1f MiB of HTML in %s", n, float64(len(body))/(1<<20), time.Since(start).Round(time.Millisecond))
	chunks := chunkOpen.FindAllStringSubmatch(body, -1)
	if len(chunks) != (n+499)/500 {
		t.Fatalf("%d lines in %d chunks", n, len(chunks))
	}
	for i, c := range chunks {
		if c[1] != strconv.Itoa(i*500+1) || c[2] != "" {
			t.Fatalf("chunk %d opens %q", i, c[0])
		}
	}
	first := body[strings.Index(body, `<pre class="text">`):]
	if text := first[:strings.Index(first, "</pre>")]; strings.Count(text, "\n") != 501 || !strings.HasPrefix(text, `<pre class="text">`+"\n"+fmt.Sprintf(logLine, 1)) {
		t.Fatalf("the first chunk does not hold lines 1 to 500")
	}
	if !strings.Contains(body, `<pre class="gut" aria-hidden="true">`+"\n501\n502\n") || !strings.Contains(body, fmt.Sprintf(logLine, n)+"</pre>") {
		t.Fatal("the gutter or the last line is missing")
	}
	next, line := marker(t, "whole", body)
	if fi, _ := os.Stat(filepath.Join(home, "reports", "t1", "build.log")); next != fi.Size() || line != n+1 {
		t.Fatalf("end marker = %d, %d", next, line)
	}
	lacks(t, "whole", body[:strings.Index(body, `<div class="lines">`)], "text-window", "data-region=")
	contains(t, "whole", body[:strings.Index(body, `<div class="lines">`)], `<div class="text-view" data-whole="true">`, `<button type="button" class="text-follow" aria-pressed="false" hidden>Follow</button>`, `<a class="text-to-end" href="#text-end">End</a>`)
}

func TestAFiftyMiBLogOpensAtItsTail(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/notes.txt": "n\n"})
	p := filepath.Join(home, "reports", "t1", "big.log")
	fail := "\x1b[31mFAILURE\x1b[0m in pkg/export"
	n := writeLog(t, p, 50, map[int]string{10: fail, 500_000: fail, 900_000: strings.ToLower(fail)})
	page := "/doc/reports/t1/big.log"
	size := int64(n) * lineLen
	view := func(t *testing.T, path string) (body string, first, last int) {
		t.Helper()
		start := time.Now()
		body = get(t, h, path)
		took := time.Since(start)
		m := windowText.FindAllStringSubmatch(body, -1)
		if len(m) != 2 {
			t.Fatalf("%s shows %d window navs", path, len(m))
		}
		first, _ = strconv.Atoi(m[0][1])
		last, _ = strconv.Atoi(m[0][2])
		if c := chunkOpen.FindStringSubmatch(body); c == nil || c[1] != strconv.Itoa(first) {
			t.Fatalf("%s: the first chunk is not line %d", path, first)
		}
		for _, k := range []int{first, last} {
			if !strings.Contains(body, fmt.Sprintf("%08d ", k)) {
				t.Fatalf("%s lacks line %d", path, k)
			}
		}
		for _, k := range []int{first - 1, last + 1} {
			if k > 0 && strings.Contains(body, fmt.Sprintf("\n%08d ", k)) {
				t.Fatalf("%s holds line %d outside %d–%d", path, k, first, last)
			}
		}
		if got := int64(last-first+1) * lineLen; got < 4<<20-lineLen || got > 4<<20+lineLen || len(body) > 6<<20 {
			t.Fatalf("%s shows %d bytes of the file in %d bytes of HTML", path, got, len(body))
		}
		t.Logf("%s: lines %d–%d in %s", path, first, last, took.Round(time.Millisecond))
		return body, first, last
	}

	tailBody, tailFirst, last := view(t, page)
	if last != n || href(tailBody, "Later") != "" || href(tailBody, "Start") != page+"?at=0" {
		t.Fatalf("the tail ends at %d of %d", last, n)
	}
	contains(t, "tail", tailBody, `<div class="text-view" data-whole="false">`, `class="text-follow"`, `<a class="text-to-end" href="#text-end">End</a>`)
	if next, line := marker(t, "tail", tailBody); next != size || line != n+1 {
		t.Fatalf("tail marker = %d, %d", next, line)
	}

	startBody, first, startLast := view(t, href(tailBody, "Start"))
	if first != 1 || href(startBody, "Start") != "" || href(startBody, "Earlier") != "" || strings.Contains(startBody, `class="text-follow"`) {
		t.Fatalf("Start opens at line %d", first)
	}
	contains(t, "start", startBody, `<a class="text-to-end" href="`+page+`#text-end">End</a>`)
	laterBody, laterFirst, _ := view(t, href(startBody, "Later"))
	if laterFirst != startLast+1 {
		t.Fatalf("Later opens at line %d after %d", laterFirst, startLast)
	}
	if _, back, _ := view(t, href(laterBody, "Earlier")); back != 1 {
		t.Fatalf("Earlier from line %d opens at line %d", laterFirst, back)
	}
	if _, _, earlierLast := view(t, href(tailBody, "Earlier")); earlierLast != tailFirst-1 {
		t.Fatalf("Earlier from the tail ends at %d, want %d", earlierLast, tailFirst-1)
	}
	if _, at, _ := view(t, page+"?line=123456"); at != 123456 {
		t.Fatalf("line 123456 opens at %d", at)
	}
	for _, q := range []string{"?line=99999999", "?at=" + strconv.FormatInt(size, 10)} {
		if _, _, end := view(t, page+q); end != n {
			t.Fatalf("%s does not open the tail", q)
		}
	}
	if _, at, _ := view(t, page+"?at=-5"); at != 1 {
		t.Fatalf("?at=-5 opens at %d", at)
	}

	found, _, _ := view(t, page+"?find=failure+IN")
	hits := found[strings.Index(found, `<div class="text-hits">`):]
	contains(t, "find", hits[:strings.Index(hits, "</div>")], `<p class="meta">3 lines hold “failure IN”.</p><ol><li><a href="`+page+`?find=failure&#43;IN&amp;line=10#L10">10</a></li><li><a href="`+page+`?find=failure&#43;IN&amp;line=500000#L500000">500000</a></li><li><a href="`+page+`?find=failure&#43;IN&amp;line=900000#L900000">900000</a></li></ol>`)
	if body := get(t, h, page+"?find=nowhere"); !strings.Contains(body, `<div class="text-hits"><p class="quiet">“nowhere” is not in this file.</p></div>`) {
		t.Fatal("a missing needle is not reported")
	}
}

func TestFindListsAtMostTwoHundredLines(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/run.log": strings.Repeat("Retry <x>\n", 250)})
	body := get(t, h, "/doc/reports/t1/run.log?find=retry+%3Cx%3E")
	contains(t, "find", body, `<p class="meta">More than 200 lines hold “retry &lt;x&gt;”; the first 200 are listed.</p><ol><li><a href="#L1">1</a></li>`, `<li><a href="#L200">200</a></li></ol>`, `<input type="search" name="find" value="retry &lt;x&gt;"`)
	lacks(t, "find", body, `href="#L201"`)
}

func follow(t *testing.T, h http.Handler, path string, next int64, line int) *httptest.ResponseRecorder {
	t.Helper()
	rec := request(h, "GET", fmt.Sprintf("%s?from=%d&line=%d", path, next, line), nil, true)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("follow Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	return rec
}

func TestFollowAppendsTheLinesAFileGains(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/run.log": "one\ntwo\nthr"})
	p, path := filepath.Join(home, "reports", "t1", "run.log"), "/doc/reports/t1/run.log"
	body := get(t, h, path)
	contains(t, "page", body, `<div class="chunk" data-line="1"><pre class="text">`+"\none\ntwo\n</pre>", `<div class="chunk" data-line="3" data-partial><pre class="text">`+"\nthr\n</pre>")
	next, line := marker(t, "page", body)
	if next != 8 || line != 3 {
		t.Fatalf("page marker = %d, %d", next, line)
	}
	if rec := follow(t, h, path, next, line); rec.Code != http.StatusNoContent || rec.Header().Get("X-Hand-Reset") != "" {
		t.Fatalf("a file that gained no whole line = %d", rec.Code)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("ee\nfour\nfi"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	rec := follow(t, h, path, next, line)
	got := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.HasPrefix(got, `<div class="chunk" data-line="3"><pre class="text">`+"\nthree\nfour\n</pre>") || strings.Contains(got, "fi\n") {
		t.Fatalf("follow = %d:\n%s", rec.Code, got)
	}
	if next, line = marker(t, "follow", got); next != 19 || line != 5 {
		t.Fatalf("follow marker = %d, %d", next, line)
	}
	if err := os.WriteFile(p, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := follow(t, h, path, next, line); rec.Header().Get("X-Hand-Reset") != "1" || rec.Body.Len() != 0 {
		t.Fatalf("a truncated file = %d %q", rec.Code, rec.Header().Get("X-Hand-Reset"))
	}
	if err := os.WriteFile(p, []byte("rotated: a new file, longer than before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := follow(t, h, path, next, line); rec.Header().Get("X-Hand-Reset") != "1" {
		t.Fatalf("a replaced file = %d, no reset", rec.Code)
	}
	for _, q := range []string{"?from=x&line=1", "?from=0&line=0", "?from=-1&line=1"} {
		if rec := request(h, "GET", path+q, nil, true); rec.Header().Get("X-Hand-Reset") != "1" {
			t.Errorf("%s = %d, no reset", q, rec.Code)
		}
	}
	if err := os.WriteFile(p, []byte("bin\x00ary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := follow(t, h, path, 0, 1); rec.Header().Get("X-Hand-Reset") != "1" {
		t.Fatalf("a binary file = %d, no reset", rec.Code)
	}
}

func TestTheLineWriterShowsWhatATerminalWouldAndEscapesTheRest(t *testing.T) {
	long := strings.Repeat("y", 2<<20)
	log := strings.Join([]string{
		"\x1b[32m✓\x1b[0m pkg/export ok",
		"\x1b[1;31mFAIL\x1b[22m still red\x1b[39m plain",
		"\x1b[38;5;32mnot green\x1b[0m \x1b[38;2;1;31;3mnot red\x1b[m",
		"\x1b]0;window title\x07after \x1b]8;;https://example.invalid/\x1b\\label\x1b]8;;\x1b\\ \x1b[2Kcleared \x1b[?25l\x1b[>4;2mkeys",
		"10%\r50%\r100%",
		"crlf\r",
		"bell\a tab\there del\x7f",
		"if (role != \"admin\u202e\u2066 \") { // u2028\u2028c1\u0085",
		"<script>alert('x')</script> & \"q\"",
		long,
		"after the long line",
		"bad \xff byte",
	}, "\n") + "\n"
	h, _ := viewer(t, map[string]string{"reports/t1/hostile.log": log})
	body := get(t, h, "/doc/reports/t1/hostile.log")
	text := body[strings.Index(body, `<div class="lines">`):strings.Index(body, `<span id="text-end"`)]
	contains(t, "hostile", strings.ReplaceAll(text, long[:maxLineForTest], "LONG"),
		"\n"+`<span class="x-g">✓</span> pkg/export ok`+"\n",
		`<span class="x-r x-bold">FAIL</span><span class="x-r"> still red</span> plain`+"\n",
		"\nnot green not red\n",
		"\nafter label cleared keys\n",
		"\n100%\n", "\ncrlf\n",
		"\nbell"+`<span class="ctl">␇</span>`+" tab\there del"+`<span class="ctl">␡</span>`+"\n",
		`if (role != &#34;admin<span class="ctl">U+202EU+2066</span> &#34;) { // u2028<span class="ctl">U+2028</span>c1<span class="ctl">U+0085</span>`+"\n",
		"\n&lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; &#34;q&#34;\n",
		"\nLONG"+`<span class="cut">… 1.8 MiB more on this line</span>`+"\nafter the long line\nbad \uFFFD byte\n",
		"\n10\n11\n12\n</pre>")
	lacks(t, "hostile", text, "\x1b", "\r", "\a", "\x7f", "\u202e", "\u2028", "\u0085", "<script", "window title", "example.invalid")
	if len(body) > 1<<20 {
		t.Fatalf("a 2 MiB line made %d bytes of HTML", len(body))
	}
	controls := strings.Repeat("x", 9000) + "\x00\x01\x02\n"
	h, _ = viewer(t, map[string]string{"reports/t1/late.log": controls})
	contains(t, "late NUL", get(t, h, "/doc/reports/t1/late.log"), `x<span class="ctl">␀␁␂</span>`+"\n</pre>")
}

const maxLineForTest = 256 << 10

type sink struct{ h http.Header }

func (s sink) Header() http.Header       { return s.h }
func (sink) Write(b []byte) (int, error) { return len(b), nil }
func (sink) WriteHeader(int)             {}

func TestALargeFileIsServedInBoundedMemory(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/notes.txt": "n\n"})
	n := writeLog(t, filepath.Join(home, "reports", "t1", "big.log"), 64, map[int]string{n64 - 5: "needle here"})
	if n != n64 {
		t.Fatalf("the 64 MiB log has %d lines, want %d", n, n64)
	}
	heap := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	for _, q := range []string{"", "?at=0", "?to=33554432", "?line=500000", "?find=needle", "?from=0&line=1", "?download"} {
		runtime.GC()
		metrics.Read(heap)
		base := heap[0].Value.Uint64()
		var peak atomic.Uint64
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			s := []metrics.Sample{{Name: heap[0].Name}}
			for {
				metrics.Read(s)
				peak.Store(max(peak.Load(), s[0].Value.Uint64()))
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Microsecond):
				}
			}
		}()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		req := httptest.NewRequest("GET", "/doc/reports/t1/big.log"+q, nil)
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
		start := time.Now()
		h.ServeHTTP(sink{http.Header{}}, req)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		close(stop)
		<-done
		grew := float64(peak.Load()-min(peak.Load(), base)) / (1 << 20)
		t.Logf("64 MiB log %-18q %6s, heap +%.1f MiB, %.1f MiB allocated", q, took.Round(time.Millisecond), grew, float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
		if grew >= 16 {
			t.Errorf("%q grew the heap by %.1f MiB", q, grew)
		}
	}
}

var n64 = int((64<<20 + lineLen - 1) / lineLen)

func TestTheTextViewIsStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".thread.doc:has(.text-view){max-width:1280px}",
		"body:not([data-page=fleet]):has(.text-view) .masthead-inner{padding-inline:max(16px,calc((100% - 1248px)/2))}",
		".text-view{display:grid;gap:8px;min-width:0}",
		".text-bar{position:sticky;top:0;z-index:2;display:flex;flex-wrap:wrap;align-items:center;gap:8px;padding:8px 0;background:var(--bg)}",
		".text-find{display:flex;flex:1 1 240px;align-items:center;gap:8px;min-width:0}",
		".text-find label{position:relative;display:block;flex:1;min-width:0;max-width:360px}",
		".text-find .icon{position:absolute;left:10px;top:50%;margin-top:-8px;color:var(--muted);pointer-events:none}",
		".text-find input[type=search]{width:100%;padding-left:34px}",
		".text-line{display:flex;align-items:center;gap:8px}",
		".text-line input{width:10ch}",
		".text-wrap[aria-pressed=true],.text-follow[aria-pressed=true]{border-color:var(--accent);background:var(--accent-bg);color:var(--accent)}",
		".text-to-end,.text-window a,.text-hits a{display:inline-flex;align-items:center;min-height:var(--control)}",
		".text-window{display:flex;flex-wrap:wrap;align-items:center;gap:0 12px}",
		".text-hits ol{display:flex;flex-wrap:wrap;gap:0 12px;margin:0;padding:0;list-style:none;font-family:var(--mono);font-size:12px}",
		".lines{min-width:0;padding:6px 0;border:1px solid var(--line);border-radius:var(--r-md);background:var(--card);overflow-x:auto}",
		".chunk{display:flex;width:max-content;min-width:100%;content-visibility:auto;contain-intrinsic-height:auto 9000px}",
		".chunk:last-of-type{content-visibility:visible}",
		".chunk>pre{overflow:visible;white-space:pre;overflow-wrap:normal;tab-size:4}",
		".chunk>.text{flex:1 0 auto;padding:0 12px}",
		".chunk>.gut{order:-1;position:sticky;left:0;flex:none;min-width:calc(6ch + 21px);padding:0 8px 0 12px;border-right:1px solid var(--line);background:var(--card);color:var(--muted);text-align:right;user-select:none;cursor:pointer}",
		".lines[data-wrap] .chunk{width:auto}",
		".lines[data-wrap] .gut{display:none}",
		".lines[data-wrap] .text{flex:1 1 auto;min-width:0;white-space:pre-wrap;overflow-wrap:anywhere}",
		".x-r{color:var(--fail)}", ".x-g{color:var(--pass)}", ".x-y{color:var(--wait)}", ".x-b,.x-m,.x-c{color:var(--accent)}", ".x-k{color:var(--muted)}", ".x-w{color:var(--fg)}", ".x-bold{font-weight:600}",
		".ctl{padding:0 2px;border:1px solid var(--line-strong);border-radius:var(--r-sm);color:var(--fail)}",
		".cut{color:var(--muted);font-style:italic}",
		"::highlight(line){background-color:var(--accent-bg);color:var(--accent)}",
		"::highlight(find){background-color:var(--wait-bg);color:var(--fg);text-decoration:underline 2px var(--wait)}",
		"::highlight(find-now){background-color:var(--wait);color:var(--bg)}")
}
