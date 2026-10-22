package board_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
)

const logLine = 64

func writeLog(t *testing.T, p string, mib int) int {
	t.Helper()
	lines := mib << 20 / logLine
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 0, 1<<20)
	for n := 1; n <= lines; n++ {
		buf = fmt.Appendf(buf, "%-*s\n", logLine-1, fmt.Sprintf("line %08d", n))
		if len(buf)+logLine > cap(buf) || n == lines {
			if _, err := f.Write(buf); err != nil {
				t.Fatal(err)
			}
			buf = buf[:0]
		}
	}
	return lines
}

func bigLog(t *testing.T, mib int) http.Handler {
	t.Helper()
	home := fleetFolder(t, map[string]string{"reports/small.txt": "x\n"})
	writeLog(t, filepath.Join(home, "reports", "big.log"), mib)
	return board.New(open(t), token, board.Options{Home: home, Controls: true})
}

var (
	fromAttr = regexp.MustCompile(`data-from="(\d+)"`)
	lineText = regexp.MustCompile(`line (\d{8}) `)
)

func firstFrom(t *testing.T, body string) int {
	t.Helper()
	m := fromAttr.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no chunk in:\n%.600s", body)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func lastLine(t *testing.T, body string) int {
	t.Helper()
	all := lineText.FindAllStringSubmatch(body, -1)
	if len(all) == 0 {
		t.Fatalf("no line in:\n%.600s", body)
	}
	n, _ := strconv.Atoi(all[len(all)-1][1])
	return n
}

func TestA5MiBFileRendersWhole(t *testing.T) {
	h := bigLog(t, 5)
	body := get(t, h, "/doc/reports/big.log")
	lines := 5 << 20 / logLine
	contains(t, "5 MiB log", body, `data-whole="true"`, `<div class="chunk" data-from="1">`, `<div class="chunk" data-from="501">`, `<div class="chunk" data-from="81501">`, fmt.Sprintf("line %08d ", lines), "line 00000001 ", `class="text-follow"`, `class="text-wrap"`, `class="text-find"`, `href="#text-end">End</a>`, `href="/doc/reports/big.log?download">Download</a>`)
	lacks(t, "5 MiB log", body, "text-window", `data-from="82001"`, "doc-notice", "style=", "<script>")
	if got, want := strings.Count(body, `<div class="chunk"`), (lines+499)/500; got != want {
		t.Errorf("%d chunks, want %d", got, want)
	}
	if n := len(body); n > 5<<20*3/2 {
		t.Errorf("page is %d bytes for a 5 MiB file", n)
	}
	contains(t, "gutter", body, "<pre class=\"gut\" aria-hidden=\"true\">1\n2\n3\n")
	lacks(t, "region", body, `data-region=`)
}

func TestA50MiBFileOpensAtItsTail(t *testing.T) {
	h := bigLog(t, 50)
	lines := 50 << 20 / logLine
	tail := get(t, h, "/doc/reports/big.log")
	contains(t, "tail", tail, `data-whole="false"`, fmt.Sprintf("line %08d ", lines), `href="/doc/reports/big.log?at=0">Start</a>`, `rel="prev">Earlier</a>`, `class="text-follow"`, `href="#text-end">End</a>`, `name="line"`, "From line ")
	lacks(t, "tail", tail, "line 00000001 ", "Later</a>", "doc-notice")
	if n := len(tail); n > 6<<20 {
		t.Errorf("the tail window is %d bytes", n)
	}
	first := firstFrom(t, tail)
	if first < lines-4<<20/logLine-2 || first > lines-4<<20/logLine+2 {
		t.Errorf("the tail starts at line %d of %d", first, lines)
	}
	start := get(t, h, "/doc/reports/big.log?at=0")
	contains(t, "start", start, `data-from="1"`, "line 00000001 ", `rel="next">Later</a>`, `href="/doc/reports/big.log">End</a>`)
	lacks(t, "start", start, ">Start</a>", "Earlier</a>", `class="text-follow"`)
	later := regexp.MustCompile(`href="([^"]+)" rel="next">Later`).FindStringSubmatch(start)
	if later == nil {
		t.Fatal("no Later link")
	}
	end := lastLine(t, start)
	next := get(t, h, strings.ReplaceAll(later[1], "&amp;", "&"))
	if got := firstFrom(t, next); got != end+1 {
		t.Errorf("Later starts at line %d, the window before it ended at %d", got, end)
	}
	contains(t, "later", next, fmt.Sprintf("line %08d ", end+1), "Earlier</a>", ">Start</a>")
	if span := end - firstFrom(t, start) + 1; span*logLine < 4<<20 || span*logLine > 4<<20+logLine {
		t.Errorf("a window holds %d lines", span)
	}
	jump := get(t, h, "/doc/reports/big.log?line=500000")
	contains(t, "line", jump, `data-from="500000"`, "line 00500000 ", "From line 500000 of a 50.0 MiB file")
	lacks(t, "line", jump, "line 00499999 ")
	if got := firstFrom(t, get(t, h, "/doc/reports/big.log?at=100")); got != 3 {
		t.Errorf("a byte in the middle of line 2 opens at line %d, want 3", got)
	}
	past := get(t, h, "/doc/reports/big.log?line=99999999")
	contains(t, "past the end", past, fmt.Sprintf("line %08d ", lines))
	for _, q := range []string{"?at=-5", "?at=abc", "?line=0", "?line=-3", "?at=999999999999"} {
		get(t, h, "/doc/reports/big.log"+q)
	}
}

func TestFindListsTheLinesOfTheWholeFile(t *testing.T) {
	h := bigLog(t, 50)
	body := get(t, h, "/doc/reports/big.log?find=LINE%2000123456")
	contains(t, "hit", body, `<a href="/doc/reports/big.log?line=123456#L123456">line 123456</a>`, `value="LINE 00123456"`)
	lacks(t, "hit", body, "matching lines", "is not in this file")
	capped := get(t, h, "/doc/reports/big.log?find=line%200")
	if got := strings.Count(capped, `?line=`); got < 200 || got > 205 {
		t.Errorf("%d hit links", got)
	}
	contains(t, "capped", capped, "The first 200 matching lines.")
	contains(t, "long needle", get(t, h, "/doc/reports/big.log?find="+strings.Repeat("a", 500)), "is not in this file.")
	contains(t, "none", get(t, h, "/doc/reports/big.log?find=nowhere"), "“nowhere” is not in this file.")
	lacks(t, "blank", get(t, h, "/doc/reports/big.log?find=%20%20"), "is not in this file", "text-hits")

	small := bigLog(t, 1)
	contains(t, "whole", get(t, small, "/doc/reports/big.log?find=line%2000000042"), `<a href="#L42">line 42</a>`)
}

func TestFollowAppendsOnlyCompleteLines(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/run.log": "one\ntwo\nthr"})
	p := filepath.Join(home, "reports", "run.log")
	contains(t, "page", get(t, h, "/doc/reports/run.log"), `data-next="8" data-line="3" data-partial="true"`, `<span class="partial">thr</span>`)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("ee\nfour\nfi"); err != nil {
		t.Fatal(err)
	}
	rec := request(h, "GET", "/doc/reports/run.log?from=8&line=3", nil, true)
	headers(t, "follow", rec, map[string]string{"X-Hand-Next": "19", "X-Hand-Line": "5", "X-Hand-Reset": "", "Cache-Control": "no-store", "Content-Type": "text/html; charset=utf-8"})
	if want := `<div class="chunk" data-from="3"><pre class="src">three` + "\nfour\n" + `</pre><pre class="gut" aria-hidden="true">3` + "\n4\n</pre></div>"; rec.Body.String() != want {
		t.Errorf("follow = %q, want %q", rec.Body, want)
	}
	rec = request(h, "GET", "/doc/reports/run.log?from=19&line=5", nil, true)
	headers(t, "nothing new", rec, map[string]string{"X-Hand-Next": "19", "X-Hand-Line": "5", "X-Hand-Reset": ""})
	if rec.Body.Len() != 0 {
		t.Errorf("a partial line was sent: %q", rec.Body)
	}
	if rec := request(h, "GET", "/doc/reports/run.log?from=8&line=3", nil, false); rec.Code != http.StatusForbidden {
		t.Errorf("follow without the cookie = %d", rec.Code)
	}
}

func TestFollowResetsWhenTheFileShrinksOrIsReplaced(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/run.log": "one\ntwo\nthree\n"})
	p := filepath.Join(home, "reports", "run.log")
	if rec := request(h, "GET", "/doc/reports/run.log?from=14&line=4", nil, true); rec.Header().Get("X-Hand-Reset") != "" || rec.Header().Get("X-Hand-Next") != "14" {
		t.Fatalf("an unchanged file = %v", rec.Header())
	}
	for name, content := range map[string]string{"truncated": "x\n", "replaced, longer": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "emptied": ""} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		rec := request(h, "GET", "/doc/reports/run.log?from=14&line=4", nil, true)
		if rec.Header().Get("X-Hand-Reset") != "1" || rec.Body.Len() != 0 || rec.Code != http.StatusOK {
			t.Errorf("%s: reset = %q, %d, %q", name, rec.Header().Get("X-Hand-Reset"), rec.Code, rec.Body)
		}
		if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"from=-1&line=1", "from=abc&line=1", "from=0&line=0", "from=999999&line=1"} {
		if rec := request(h, "GET", "/doc/reports/run.log?"+q, nil, true); rec.Code != http.StatusOK {
			t.Errorf("%s = %d", q, rec.Code)
		}
	}
}

func TestFollowOnABinaryFileShowsThePage(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/a.bin": "\x00\x01\x02"})
	body := get(t, h, "/doc/reports/a.bin?from=0&line=1")
	contains(t, "binary", body, "This file is not text", `href="/doc/reports/a.bin?download"`)
}

func TestAnEmptyFileSaysSo(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/empty.log": ""})
	contains(t, "empty", get(t, h, "/doc/reports/empty.log"), "This file is empty.", `class="text-follow"`, `data-next="0" data-line="1"`)
}

func TestTextPastTheFirstKiBsIsShownWithVisibleSymbols(t *testing.T) {
	body := strings.Repeat("abc\n", 4096) + "x\x00y\xffz\n"
	h, _ := viewer(t, map[string]string{"reports/late.log": body})
	got := get(t, h, "/doc/reports/late.log")
	contains(t, "late", got, `x<span class="ctl" title="control character 0x00">␀</span>y`+"�"+"z\n")
	lacks(t, "late", got, "doc-notice")
	head := strings.Repeat("a", 100) + "\x00" + strings.Repeat("b\n", 20000)
	h, _ = viewer(t, map[string]string{"reports/early.log": head})
	contains(t, "early", get(t, h, "/doc/reports/early.log"), "This file is not text", "?download")
}

func TestHostileTextNeverBecomesMarkup(t *testing.T) {
	evil := "<img src=x onerror=alert(1)>\n<script>alert(2)</script>\n\x1b[31m<b>red</b>\x1b[0m\n\"><svg onload=alert(3)>\n&lt;i&gt; &#60;u&#62;\n\x1b]8;;javascript:alert(4)\x07link\x1b]8;;\x07\n\u202e<a href=//evil>\n"
	h, _ := viewer(t, map[string]string{"reports/evil.log": evil})
	rec := request(h, "GET", "/doc/reports/evil.log", nil, true)
	headers(t, "evil", rec, map[string]string{"Content-Type": "text/html; charset=utf-8", "X-Frame-Options": "DENY"})
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("text page CSP = %q", csp)
	}
	body := rec.Body.String()
	lacks(t, "evil", body, "<img", "<script>", "<b>", "<svg onload", "<a href=//", "javascript:", "<i>", "<u>")
	contains(t, "evil", body, "&lt;img src=x onerror=alert(1)&gt;", "&lt;script&gt;alert(2)&lt;/script&gt;", `<span class="x-r">&lt;b&gt;red&lt;/b&gt;</span>`, "&amp;lt;i&amp;gt;", "&amp;#60;u&amp;#62;", "link\n", `title="bidirectional control U+202E">U+202E</span>&lt;a href=//evil&gt;`)
}

func TestATextPageIsNotAnAutoRefreshedRegion(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/a.log": "x\n", "reports/t1/design.md": design})
	contains(t, "text", get(t, h, "/doc/reports/a.log"), `<main class="thread doc">`)
	contains(t, "markdown", get(t, h, "/doc/reports/t1/design.md"), `<main class="thread doc" data-region="doc">`)
}

func TestTheTextViewIsStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".thread.doc:has(.text-view){max-width:1280px}",
		".text-view{display:grid;gap:8px;min-width:0}",
		".text-bar{position:sticky;top:var(--mast);z-index:2;display:flex;flex-wrap:wrap;align-items:center;gap:8px;padding:6px 0;background:var(--bg)}",
		".text-find{display:flex;flex:1 1 260px;align-items:center;gap:8px;min-width:0}",
		".text-find input[type=search]{width:100%;padding-left:34px}",
		".text-follow[aria-pressed=true],.text-wrap[aria-pressed=true],.doc-wide[aria-pressed=true],.table-pin[aria-pressed=true]{border-color:var(--accent);background:var(--accent-bg);color:var(--accent)}",
		".text-end-link,.text-window>a,.text-hits a{display:inline-flex;align-items:center;min-height:var(--control)}",
		".lines{min-width:0;padding:6px 0;border:1px solid var(--line);border-radius:var(--r-md);background:var(--card);font-family:var(--mono);font-size:12px;line-height:18px;overflow-x:auto}",
		".lines pre{margin:0;font:inherit}",
		".chunk{position:relative;display:grid;grid-template-columns:minmax(0,1fr);width:max-content;min-width:100%;content-visibility:auto;contain-intrinsic-size:auto 9000px}",
		".chunk>.gut{grid-area:1/1;justify-self:start;position:sticky;left:0;z-index:1;width:calc(8ch + 22px);padding:0 10px 0 12px;background:var(--card);color:var(--muted);text-align:right;user-select:none;cursor:pointer}",
		".chunk>.src{grid-area:1/1;padding:0 12px 0 calc(8ch + 22px);white-space:pre;tab-size:4}",
		".lines[data-wrap] .chunk{width:auto}",
		".lines[data-wrap] .gut{display:none}",
		".lines[data-wrap] .src{padding-left:12px;white-space:pre-wrap;overflow-wrap:anywhere}",
		".here{position:absolute;left:0;right:0;z-index:-1;height:18px;background:var(--wait-bg);pointer-events:none}",
		".x-r{color:var(--fail)}", ".x-g{color:var(--pass)}", ".x-y{color:var(--wait)}",
		".x-b,.x-m,.x-c{color:var(--accent)}", ".x-k,.x-w{color:var(--muted)}", ".x-bold{font-weight:600}",
		".ctl{padding:0 2px;border:1px solid var(--line-strong);border-radius:var(--r-sm);color:var(--fail)}",
		".cut{color:var(--muted);font-style:italic}",
		"::highlight(find){background:var(--wait-bg);color:var(--fg)}",
		"::highlight(find-now){background:var(--wait);color:var(--bg)}",
		"@media (forced-colors:active){.here{border-block:1px solid Highlight}}")
}
