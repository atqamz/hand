package board_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const (
	mp4  = "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom\x00\x00\x00\x08free"
	webm = "\x1a\x45\xdf\xa3\x93\x42\x82\x88matroska"
	mp3  = "ID3\x03\x00\x00\x00\x00\x00\x00"
	wav  = "RIFF\x24\x00\x00\x00WAVEfmt "
	ogg  = "OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00"
	pdf  = "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"
	svg  = `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><rect width="4" height="4"/></svg>`
	page = "<!doctype html><title>x</title><p>static</p><script>document.body.append('RAN');fetch('/')</script><img src=/doc/reports/t1/flow.png>"
)

func TestRawHeadersPerType(t *testing.T) {
	files := map[string]string{
		"p/page.html": page, "p/page.HTM": page, "p/x.svg": svg, "p/spec.pdf": pdf,
		"p/a.mp4": mp4, "p/a.webm": webm, "p/a.mp3": mp3, "p/a.wav": wav, "p/a.ogg": ogg,
		"p/flow.png": string(flowPNG), "p/fake.png": "<html><script>alert(1)</script>\n", "p/app.js": "alert(document.cookie)\n",
		"p/note.md": "# note\n", "p/data.json": "{}\n", "p/z.zip": "PK\x03\x04\x00\x00\x00\x00",
	}
	h, _ := viewer(t, files)
	const sandboxed = "sandbox; default-src 'none'"
	for rel, want := range map[string]map[string]string{
		"p/page.html": {"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": sandboxed + "; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'", "X-Frame-Options": "SAMEORIGIN"},
		"p/page.HTM":  {"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": sandboxed + "; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'", "X-Frame-Options": "SAMEORIGIN"},
		"p/x.svg":     {"Content-Type": "image/svg+xml", "Content-Security-Policy": sandboxed + "; style-src 'unsafe-inline'", "X-Frame-Options": "DENY"},
		"p/spec.pdf":  {"Content-Type": "application/pdf", "Content-Security-Policy": sandboxed, "X-Frame-Options": "SAMEORIGIN"},
		"p/a.mp4":     {"Content-Type": "video/mp4", "Content-Security-Policy": sandboxed, "X-Frame-Options": "DENY"},
		"p/a.webm":    {"Content-Type": "video/webm", "Content-Security-Policy": sandboxed},
		"p/a.mp3":     {"Content-Type": "audio/mpeg", "Content-Security-Policy": sandboxed},
		"p/a.wav":     {"Content-Type": "audio/wave", "Content-Security-Policy": sandboxed},
		"p/a.ogg":     {"Content-Type": "audio/ogg", "Content-Security-Policy": sandboxed},
		"p/flow.png":  {"Content-Type": "image/png", "Content-Security-Policy": sandboxed, "X-Frame-Options": "DENY"},
		"p/fake.png":  {"Content-Type": "text/plain; charset=utf-8", "Content-Security-Policy": sandboxed, "X-Frame-Options": "DENY"},
		"p/app.js":    {"Content-Type": "text/plain; charset=utf-8", "Content-Security-Policy": sandboxed, "X-Frame-Options": "DENY"},
		"p/note.md":   {"Content-Type": "text/plain; charset=utf-8", "Content-Security-Policy": sandboxed},
		"p/data.json": {"Content-Type": "text/plain; charset=utf-8", "Content-Security-Policy": sandboxed},
		"p/z.zip":     {"Content-Type": "application/octet-stream", "Content-Disposition": "attachment; filename=z.zip", "Content-Security-Policy": sandboxed},
	} {
		rec := request(h, "GET", "/doc/"+rel+"?raw", nil, true)
		if rec.Code != http.StatusOK || rec.Body.String() != files[rel] {
			t.Errorf("%s = %d", rel, rec.Code)
		}
		want["X-Content-Type-Options"], want["Cache-Control"] = "nosniff", "private, no-cache"
		headers(t, rel, rec, want)
		csp := rec.Header().Get("Content-Security-Policy")
		for _, bad := range []string{"allow-scripts", "allow-same-origin", "script-src", "unsafe-eval"} {
			if strings.Contains(csp, bad) {
				t.Errorf("%s CSP %q holds %q", rel, csp, bad)
			}
		}
		if !strings.HasPrefix(csp, "sandbox;") {
			t.Errorf("%s CSP %q does not start with sandbox", rel, csp)
		}
	}
}

func TestOnlyHTMLAndPDFMayBeFramedAndOnlyBySameOrigin(t *testing.T) {
	h, _ := viewer(t, map[string]string{"p/page.html": page, "p/spec.pdf": pdf, "p/x.svg": svg, "p/a.log": "x\n"})
	for rel, framed := range map[string]bool{"p/page.html": true, "p/spec.pdf": true, "p/x.svg": false, "p/a.log": false} {
		rec := request(h, "GET", "/doc/"+rel+"?raw", nil, true)
		want := "DENY"
		if framed {
			want = "SAMEORIGIN"
		}
		if got := rec.Header().Get("X-Frame-Options"); got != want {
			t.Errorf("%s X-Frame-Options = %q, want %q", rel, got, want)
		}
	}
	if csp := request(h, "GET", "/doc/p/page.html?raw", nil, true).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("html CSP = %q", csp)
	}
}

func TestTheRawRouteKeepsTheViewersRefusals(t *testing.T) {
	h, _ := viewer(t, map[string]string{"hand.db": secret, "reports/.draft.md": secret, "reports/a.log": "x\n"})
	for _, p := range []string{"/doc/hand.db?raw", "/doc/reports/.draft.md?raw", "/doc/..%2Fhand.db?raw", "/doc/luvus/x?raw"} {
		if rec := request(h, "GET", p, nil, true); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	if rec := request(h, "GET", "/doc/reports/a.log?raw", nil, false); rec.Code == http.StatusOK {
		t.Errorf("raw without the cookie = %d", rec.Code)
	}
	if rec := request(h, "GET", "/doc/reports?raw", nil, true); rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(rec.Body.String(), `class="listing"`) {
		t.Errorf("raw on a folder = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestTheHTMLPreviewRunsNothing(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/page.html": page})
	body := get(t, h, "/doc/reports/t1/page.html")
	frame := regexp.MustCompile(`<iframe[^>]*>`).FindString(body)
	if !strings.Contains(frame, `src="/doc/reports/t1/page.html?raw"`) || !regexp.MustCompile(`\ssandbox(\s|>)`).MatchString(frame) {
		t.Fatalf("iframe = %q", frame)
	}
	for _, bad := range []string{"allow-", "srcdoc", "<script>document", "RAN", "fetch("} {
		if strings.Contains(body, bad) {
			t.Errorf("page holds %q", bad)
		}
	}
	contains(t, "html page", body, `<p class="meta">Preview without scripts</p>`, `<span class="on" aria-current="true">Preview</span><a href="/doc/reports/t1/page.html?as=text">Source</a>`, `<a class="doc-download"`)
	lacks(t, "html page", body, `data-region="doc"`)
	src := get(t, h, "/doc/reports/t1/page.html?as=text")
	contains(t, "html source", src, `<a href="/doc/reports/t1/page.html">Preview</a>`, `<span class="on" aria-current="true">Source</span>`, `&lt;script&gt;document.body.append(&#39;RAN&#39;);fetch(&#39;/&#39;)&lt;/script&gt;`, `<pre class="src">`)
	lacks(t, "html source", src, "<iframe", "<script>document", "<p>static")
}

func TestAnSVGIsOnlyEverAPictureOrText(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/x.svg": svg})
	body := get(t, h, "/doc/reports/t1/x.svg")
	contains(t, "svg page", body, `<div class="doc-picture"><img src="/doc/reports/t1/x.svg?raw" alt="x.svg"></div>`, `<span class="on" aria-current="true">Picture</span><a href="/doc/reports/t1/x.svg?as=text">Source</a>`)
	lacks(t, "svg page", body, `<svg xmlns="http://www.w3.org/2000/svg" onload`, "<rect", "<script>alert", "<iframe", "<object", "<embed")
	rec := request(h, "GET", "/doc/reports/t1/x.svg", nil, true)
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("the svg page is %q", rec.Header().Get("Content-Type"))
	}
	src := get(t, h, "/doc/reports/t1/x.svg?as=text")
	contains(t, "svg source", src, `&lt;svg xmlns=&#34;http://www.w3.org/2000/svg&#34; onload=&#34;alert(1)&#34;&gt;&lt;script&gt;alert(2)&lt;/script&gt;`)
	lacks(t, "svg source", src, `<svg xmlns="http://www.w3.org/2000/svg" onload`, "<rect", "<img src=\"/doc/reports/t1/x.svg?raw\"", "<script>alert")
	if got := request(h, "GET", "/doc/reports/t1/x.svg?download", nil, true).Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("svg download = %q", got)
	}
}

func TestPDFOpensInASameOriginFrameWithDownload(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/spec.pdf": pdf, "reports/t1/fake.pdf": "not a pdf\n"})
	body := get(t, h, "/doc/reports/t1/spec.pdf")
	contains(t, "pdf page", body, `<iframe class="doc-frame" src="/doc/reports/t1/spec.pdf?raw" title="spec.pdf"></iframe>`, `href="/doc/reports/t1/spec.pdf?download"`)
	lacks(t, "pdf page", body, "Preview without scripts", "doc-toggle", " sandbox>")
	contains(t, "fake pdf", get(t, h, "/doc/reports/t1/fake.pdf"), `<pre class="src">`)
	if rec := request(h, "GET", "/doc/reports/t1/fake.pdf?raw", nil, true); rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("fake pdf raw = %q", rec.Header().Get("Content-Type"))
	}
}

func TestMediaPlaysOnlyWhenTheBytesAndTheNameAgree(t *testing.T) {
	files := map[string]string{
		"m/a.mp4": mp4, "m/a.webm": webm, "m/a.mp3": mp3, "m/a.wav": wav, "m/a.ogg": ogg, "m/a.OPUS": ogg,
		"m/lies.txt": mp4, "m/webm-as.mp4": webm, "m/text.mp4": "just words\n", "m/text.mp3": "just words\n",
	}
	h, _ := viewer(t, files)
	for rel, tag := range map[string]string{"m/a.mp4": "video", "m/a.webm": "video", "m/a.mp3": "audio", "m/a.wav": "audio", "m/a.ogg": "audio", "m/a.OPUS": "audio"} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body, fmt.Sprintf(`<div class="doc-media"><%s controls preload="metadata" src="/doc/%s?raw"></%s></div>`, tag, rel, tag), "doc-download")
		lacks(t, rel, body, `data-region="doc"`, "doc-notice")
	}
	for _, rel := range []string{"m/lies.txt", "m/webm-as.mp4"} {
		body := get(t, h, "/doc/"+rel)
		lacks(t, rel, body, "<video", "<audio")
		contains(t, rel, body, "This file is not text")
		rec := request(h, "GET", "/doc/"+rel+"?raw", nil, true)
		headers(t, rel, rec, map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": "attachment; filename=" + strings.TrimPrefix(rel, "m/")})
	}
	for _, rel := range []string{"m/text.mp4", "m/text.mp3"} {
		body := get(t, h, "/doc/"+rel)
		lacks(t, rel, body, "<video", "<audio")
		contains(t, rel, body, `<pre class="src">`)
		if got := request(h, "GET", "/doc/"+rel+"?raw", nil, true).Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("%s raw = %q", rel, got)
		}
	}
}

func TestMediaAnswersRangeRequests(t *testing.T) {
	body := mp4 + strings.Repeat("0123456789", 100)
	h, _ := viewer(t, map[string]string{"m/a.mp4": body})
	req := httptest.NewRequest("GET", "/doc/m/a.mp4?raw", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	req.Header.Set("Range", "bytes=10-19")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != body[10:20] {
		t.Fatalf("range = %d %q", rec.Code, rec.Body)
	}
	headers(t, "range", rec, map[string]string{"Content-Range": fmt.Sprintf("bytes 10-19/%d", len(body)), "Content-Type": "video/mp4", "X-Content-Type-Options": "nosniff"})
	if rec := request(h, "GET", "/doc/m/a.mp4?raw", nil, true); rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges = %q", rec.Header().Get("Accept-Ranges"))
	}
	req.Header.Set("Range", "bytes=99999-")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("range past the end = %d", rec.Code)
	}
}

func TestJSONIsPrettyPrintedWithAnAsWrittenToggle(t *testing.T) {
	h, _ := viewer(t, map[string]string{"d/rows.json": `{"rows":[1,2,{"k":"<b>&"}],"ok":true}` + "\n"})
	body := get(t, h, "/doc/d/rows.json")
	contains(t, "pretty", body, `<span class="on" aria-current="true">Pretty</span><a href="/doc/d/rows.json?as=text">As written</a>`, `data-whole="true"`, "{\n  &#34;rows&#34;: [\n    1,\n    2,\n    {\n      &#34;k&#34;: &#34;&lt;b&gt;&amp;&#34;\n", `<pre class="gut" aria-hidden="true">1`)
	lacks(t, "pretty", body, "<b>", `class="text-follow"`, "doc-notice")
	raw := get(t, h, "/doc/d/rows.json?as=text")
	contains(t, "as written", raw, `<a href="/doc/d/rows.json">Pretty</a><span class="on" aria-current="true">As written</span>`, `{&#34;rows&#34;:[1,2,{&#34;k&#34;:&#34;&lt;b&gt;&amp;&#34;}],&#34;ok&#34;:true}`, `<input type="hidden" name="as" value="text">`)
	if rec := request(h, "GET", "/doc/d/rows.json?raw", nil, true); rec.Body.String() != `{"rows":[1,2,{"k":"<b>&"}],"ok":true}`+"\n" {
		t.Errorf("raw json = %q", rec.Body)
	}
}

func TestPrettyJSONOverEightMiBIsWindowed(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 6 MiB")
	}
	big := "[" + strings.Repeat("1,", (6<<20)/2) + "1]\n"
	deep := strings.Repeat("[", 8000) + strings.Repeat("]", 8000) + "\n"
	h, _ := viewer(t, map[string]string{"d/big.json": big, "d/deep.json": deep})
	body := get(t, h, "/doc/d/big.json")
	contains(t, "big pretty", body, `data-whole="false"`, `<span class="on" aria-current="true">Pretty</span>`, "the Board shows about 4 MiB at a time", "  1,\n  1,\n")
	lacks(t, "big pretty", body, "doc-notice", `class="text-follow"`)
	if len(body) > 16<<20 {
		t.Errorf("the page is %d bytes", len(body))
	}
	contains(t, "start", get(t, h, "/doc/d/big.json?at=0"), `<pre class="src">[`+"\n  1,\n  1,", `href="/doc/d/big.json?at=`)
	contains(t, "earlier", get(t, h, "/doc/d/big.json?at=4194304"), `href="/doc/d/big.json?at=0"`)
	deepPage := get(t, h, "/doc/d/deep.json")
	contains(t, "deep", deepPage, "This JSON is too large to pretty-print, so the Board shows it as written.", "[[[[")
	lacks(t, "deep", deepPage, "doc-toggle")
}

func TestInvalidJSONFallsBackToText(t *testing.T) {
	h, _ := viewer(t, map[string]string{"d/bad.json": `{"a": [1, 2,}` + "\n", "d/two.json": `{"a":1}` + "\n" + `{"b":2}` + "\n", "d/empty.json": ""})
	for _, rel := range []string{"d/bad.json", "d/two.json"} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body, "This is not valid JSON, so the Board shows it as written.", `<pre class="src">`)
		lacks(t, rel, body, "doc-toggle")
	}
	contains(t, "bad", get(t, h, "/doc/d/bad.json"), `{&#34;a&#34;: [1, 2,}`)
	contains(t, "empty", get(t, h, "/doc/d/empty.json"), "This file is empty.")
}

func TestJSONOverTheCapIsShownAsText(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 17 MiB")
	}
	big := "[" + strings.Repeat("1,", (17<<20)/2) + "1]\n"
	h, _ := viewer(t, map[string]string{"d/big.json": big})
	body := get(t, h, "/doc/d/big.json")
	contains(t, "big json", body, `data-whole="false"`, "the Board shows about 4 MiB at a time", `<pre class="src">`)
	lacks(t, "big json", body, "doc-toggle", "doc-notice")
	if len(body) > 6<<20 {
		t.Errorf("the page is %d bytes", len(body))
	}
}

func TestCSVIsATableWithNumbersOnTheRight(t *testing.T) {
	csv := "name,qty,note\n\"Smith, J\",12,\"said \"\"hi\"\"\"\n\"two\nlines\",-3.5,<b>x</b>\nshort\nlong,1e3,a,extra\nempty,,\n"
	h, _ := viewer(t, map[string]string{"d/rows.csv": csv, "d/rows.tsv": "a\tb\nx y\t7\n"})
	body := get(t, h, "/doc/d/rows.csv")
	contains(t, "csv", body,
		`<span class="on" aria-current="true">Table</span><a href="/doc/d/rows.csv?as=text">Text</a>`,
		`<thead><tr><th>name</th><th class="r">qty</th><th>note</th></tr></thead>`,
		`<td>Smith, J</td><td class="r">12</td><td>said &#34;hi&#34;</td>`,
		"<td>two\nlines</td><td class=\"r\">-3.5</td><td>&lt;b&gt;x&lt;/b&gt;</td>",
		`<tr><td>short</td></tr>`,
		`<td>long</td><td class="r">1e3</td><td>a</td><td>extra</td>`,
		`<tr><td>empty</td><td class="r"></td><td></td></tr>`)
	lacks(t, "csv", body, "<b>x", "The first", `data-region="doc"`)
	contains(t, "tsv", get(t, h, "/doc/d/rows.tsv"), `<th>a</th><th class="r">b</th>`, `<td>x y</td><td class="r">7</td>`)
	text := get(t, h, "/doc/d/rows.csv?as=text")
	contains(t, "csv text", text, `<a href="/doc/d/rows.csv">Table</a><span class="on" aria-current="true">Text</span>`, `<pre class="src">name,qty,note`)
	lacks(t, "csv text", text, "<table")
}

func TestCSVShowsTheHeaderAndTheFirst2000Rows(t *testing.T) {
	var b strings.Builder
	b.WriteString("n,v\n")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&b, "%d,r%d\n", i, i)
	}
	h, _ := viewer(t, map[string]string{"d/exact.csv": b.String(), "d/more.csv": b.String() + "2001,r2001\n2002,r2002\n"})
	exact := get(t, h, "/doc/d/exact.csv")
	if n := strings.Count(exact, "<tr>"); n != 2001 {
		t.Errorf("a header and 2000 rows show %d table rows", n)
	}
	contains(t, "exact", exact, "r2000", "<td class=\"r\">2000</td>")
	lacks(t, "exact", exact, "rows are shown")
	more := get(t, h, "/doc/d/more.csv")
	if n := strings.Count(more, "<tr>"); n != 2001 {
		t.Errorf("a longer file shows %d table rows", n)
	}
	contains(t, "more", more, `<p class="meta">The first 2,000 rows are shown. Text shows the whole file.</p>`, "r2000")
	lacks(t, "more", more, "r2001", "r2002")
}

func TestAHugeCSVCellIsClippedAndTheFileStaysBounded(t *testing.T) {
	huge := strings.Repeat("é", 3<<20)
	h, _ := viewer(t, map[string]string{
		"d/huge.csv":  "a,b\n1,\"" + huge + "\"\n2,fine\n",
		"d/open.csv":  "a,b\n1,\"" + strings.Repeat("x", 9<<20),
		"d/wide.csv":  "a\n" + strings.Repeat("y", 400) + "\n",
		"d/bidi.csv":  "a\n‮evil\n",
		"d/empty.csv": "",
	})
	body := get(t, h, "/doc/d/huge.csv")
	contains(t, "huge", body, "<td>"+strings.Repeat("é", 300)+"…</td>", "<td>fine</td>")
	if len(body) > 64<<10 {
		t.Errorf("the page is %d bytes", len(body))
	}
	contains(t, "wide", get(t, h, "/doc/d/wide.csv"), "<td>"+strings.Repeat("y", 300)+"…</td>")
	contains(t, "bidi", get(t, h, "/doc/d/bidi.csv"), "<td>U&#43;202Eevil</td>")
	contains(t, "open", get(t, h, "/doc/d/open.csv"), "Text shows the whole file.")
	contains(t, "empty", get(t, h, "/doc/d/empty.csv"), "This file is empty.")
	lacks(t, "empty", get(t, h, "/doc/d/empty.csv"), "doc-toggle")
}

func TestDiffsGetLineClasses(t *testing.T) {
	patch := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@ func <T>\n keep\n-old <b>\n+new\n\\ No newline at end of file\n"
	h, _ := viewer(t, map[string]string{"reports/fix.patch": patch, "reports/fix.DIFF": patch, "reports/fix.txt": patch})
	for _, rel := range []string{"reports/fix.patch", "reports/fix.DIFF"} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body,
			`<span class="d-file">diff --git a/x b/x`+"\n</span>", `<span class="d-file">index 1..2 100644`+"\n</span>",
			`<span class="d-file">--- a/x`+"\n</span>", `<span class="d-file">+++ b/x`+"\n</span>",
			`<span class="d-hunk">@@ -1,2 +1,2 @@ func &lt;T&gt;`+"\n</span>",
			` keep`+"\n"+`<span class="d-del">-old &lt;b&gt;`+"\n</span>", `<span class="d-add">+new`+"\n</span>", `\ No newline at end of file`+"\n")
		lacks(t, rel, body, "<b>", "doc-toggle")
	}
	lacks(t, "txt", get(t, h, "/doc/reports/fix.txt"), "d-add", "d-del", "d-hunk")
}

func TestDiffClassesSurviveFollow(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/fix.patch": "+a\n-b\n"})
	body := get(t, h, "/doc/reports/fix.patch?from=0&line=1")
	contains(t, "follow", body, `<span class="d-add">+a`+"\n</span>", `<span class="d-del">-b`+"\n</span>")
}

func TestTextWindowLinksKeepTheSourceView(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 9 MiB")
	}
	line := "<p>row</p>\n"
	h, _ := viewer(t, map[string]string{"r/page.html": strings.Repeat(line, (9<<20)/len(line)+1)})
	body := get(t, h, "/doc/r/page.html?as=text")
	contains(t, "window", body, `href="/doc/r/page.html?as=text&amp;at=0"`, `<input type="hidden" name="as" value="text">`, `href="/doc/r/page.html?as=text&amp;at=`)
	lacks(t, "window", body, "<iframe", `href="/doc/r/page.html?at=`)
	contains(t, "hits", get(t, h, "/doc/r/page.html?as=text&find=row"), `href="/doc/r/page.html?as=text&amp;line=1#L1"`)
}

func TestPreviewPagesAreNotAutoRefreshedRegions(t *testing.T) {
	h, _ := viewer(t, map[string]string{"p/page.html": page, "p/x.svg": svg, "p/spec.pdf": pdf, "p/a.mp4": mp4, "p/t.csv": "a\n1\n", "p/d.json": "{}", "p/z.zip": "PK\x03\x04\x00"})
	for _, rel := range []string{"p/page.html", "p/x.svg", "p/spec.pdf", "p/a.mp4", "p/t.csv", "p/d.json"} {
		lacks(t, rel, get(t, h, "/doc/"+rel), `data-region=`)
	}
	contains(t, "zip", get(t, h, "/doc/p/z.zip"), `<main class="thread doc" data-region="doc">`)
}

func TestThePreviewsAreStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".thread.doc:has(.doc-frame,.doc-table){max-width:1280px}",
		".doc-toggle{display:inline-flex;border:1px solid var(--line-strong);border-radius:var(--r-md);overflow:hidden}",
		".doc-toggle>*{display:inline-flex;align-items:center;min-height:var(--control);padding:0 12px}",
		".doc-toggle>.on{background:var(--card);color:var(--fg);font-weight:600}",
		".doc-toggle>a:hover{background:var(--hover);text-decoration:none}",
		".doc-frame{display:block;width:100%;height:75vh;min-height:320px;border:1px solid var(--line);border-radius:var(--r-md);background:#fff}",
		".doc-media video,.doc-picture img{display:block;max-width:100%;max-height:75vh;border:1px solid var(--line);border-radius:var(--r-md);background:var(--card)}",
		".doc-media audio{width:100%}",
		".doc-table .table{max-height:75vh;overflow:auto}",
		".doc-table th,.doc-table td{max-width:48ch;overflow-wrap:anywhere}",
		".doc-table thead th{position:sticky;top:0;z-index:2;white-space:normal}",
		".d-add,.d-del,.d-hunk,.d-file{display:block}",
		".d-add{background:var(--pass-bg)}", ".d-del{background:var(--fail-bg)}", ".d-hunk{color:var(--accent)}", ".d-file{font-weight:600}")
}
