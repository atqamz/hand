package board_test

import (
	"strings"
	"testing"
)

const mdToggle = `<span class="doc-toggle" role="group" aria-label="View" data-remember>`

func TestAMarkdownFileHasARenderedAndSourceToggle(t *testing.T) {
	h, _ := viewer(t, map[string]string{"r/a.md": design, "r/b.markdown": design, "r/c.txt": "x\n", "r/d.json": "{}", "r/e.csv": "a\n1\n"})
	for _, rel := range []string{"r/a.md", "r/b.markdown"} {
		page := "/doc/" + rel
		rendered := get(t, h, page)
		contains(t, rel, rendered, mdToggle+`<span class="on" aria-current="true">Rendered</span><a href="`+page+`?as=text">Source</a></span>`, `<article class="doc-body copy md">`, `data-region="doc"`)
		lacks(t, rel, rendered, `class="text-view"`)
		source := get(t, h, page+"?as=text")
		contains(t, rel, source, mdToggle+`<a href="`+page+`">Rendered</a><span class="on" aria-current="true">Source</span></span>`, `class="text-view"`, `data-whole="true"`, `id="text-lines"`, `<input type="hidden" name="as" value="text">`, `class="doc-wide"`)
		lacks(t, rel, source, `class="doc-body`, `data-region="doc"`, `<h3>Export design</h3>`, `<img src="images/flow.png"`, `<a href="api.md">`)
	}
	for _, rel := range []string{"r/c.txt", "r/d.json", "r/e.csv"} {
		lacks(t, rel, get(t, h, "/doc/"+rel), "data-remember")
	}
}

func TestMarkdownSourceIsTheTextViewerAndStaysEscaped(t *testing.T) {
	src := "# T\n\n<script>alert(1)</script>\n<img src=x onerror=alert(1)>\n\x1b[31m<b>red</b>\x1b[0m ‮<i>\n![x](a.png)\n"
	h, _ := viewer(t, map[string]string{"r/a.md": src})
	body := get(t, h, "/doc/r/a.md?as=text&find=script")
	contains(t, "source", body,
		`&lt;script&gt;alert(1)&lt;/script&gt;`, `&lt;img src=x onerror=alert(1)&gt;`, `<span class="x-r">&lt;b&gt;red&lt;/b&gt;`, `U+202E`,
		`href="#L3"`, `<pre class="gut" aria-hidden="true">1`,
		`class="text-wrap"`, `class="text-follow"`, `data-copy="contents" data-src="/doc/r/a.md?raw">Copy contents`)
	lacks(t, "source", body, "<script>alert", "<img src=x", "<img src=\"a.png\"", "<h3>")
	raw := request(h, "GET", "/doc/r/a.md?raw", nil, true)
	if raw.Body.String() != src {
		t.Errorf("?raw = %q, want the file", raw.Body.String())
	}
}

func TestMarkdownNeighboursKeepTheSourceView(t *testing.T) {
	h, _ := viewer(t, map[string]string{"r/a.md": "a\n", "r/b.md": "b\n", "r/c.txt": "c\n", "r/d.markdown": "d\n"})
	source := get(t, h, "/doc/r/b.md?as=text")
	contains(t, "source", source, `<a href="/doc/r/a.md?as=text" rel="prev"`, `<a href="/doc/r/c.txt" rel="next"`)
	lacks(t, "source", source, `c.txt?as=text`)
	contains(t, "source d", get(t, h, "/doc/r/c.txt"), `<a href="/doc/r/b.md" rel="prev"`, `<a href="/doc/r/d.markdown" rel="next"`)
	rendered := get(t, h, "/doc/r/b.md")
	contains(t, "rendered", rendered, `<a href="/doc/r/a.md" rel="prev"`)
	lacks(t, "rendered", rendered, `as=text`+`" rel`)
	contains(t, "last", get(t, h, "/doc/r/d.markdown?as=text"), `<a href="/doc/r/c.txt" rel="prev"`)
	lacks(t, "text page", get(t, h, "/doc/r/c.txt?as=text"), `?as=text" rel`)
}

func TestMarkdownOverOneMiBAndBinaryMarkdownKeepTheirBehaviour(t *testing.T) {
	line := "2026-10-19T01:00:00Z export worker heartbeat ok\n"
	big := strings.Repeat(line, (1<<20)/len(line)+1) + "TAIL-MARKER"
	h, _ := viewer(t, map[string]string{"r/huge.md": big, "r/binary.md": "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR# not a heading", "r/late.md": strings.Repeat("a\n", 5000) + "caf\xe9\n"})
	for _, page := range []string{"/doc/r/huge.md", "/doc/r/huge.md?as=text"} {
		body := get(t, h, page)
		contains(t, page, body, "TAIL-MARKER", `class="text-view"`)
		lacks(t, page, body, "doc-toggle", "data-remember", "doc-notice")
	}
	const notice = `<p class="doc-notice">This file is not text, so the Board does not show it; download it to open it.</p>`
	for _, page := range []string{"/doc/r/binary.md", "/doc/r/binary.md?as=text"} {
		body := get(t, h, page)
		contains(t, page, body, notice)
		lacks(t, page, body, "doc-toggle", `class="text-view"`, "not a heading")
	}
	late := get(t, h, "/doc/r/late.md")
	contains(t, "late", late, notice)
	lacks(t, "late", late, "doc-toggle")
}

func TestMarkdownOverEightMiBOpensItsSourceAtTheWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 9 MiB")
	}
	line := "row <b>x</b>\n"
	big := strings.Repeat(line, (9<<20)/len(line)+1) + "TAIL-MARKER\n"
	h, _ := viewer(t, map[string]string{"r/big.md": big})
	body := get(t, h, "/doc/r/big.md?as=text")
	contains(t, "window", body, "TAIL-MARKER", `class="text-window"`, `href="/doc/r/big.md?as=text&amp;at=0"`, `<input type="hidden" name="as" value="text">`, `>Copy window</button>`)
	lacks(t, "window", body, "doc-toggle", `data-whole="true"`, "<b>x</b>")
}

func TestTheMarkdownViewIsRememberedInOneGuardedKey(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const sourceKey = "hand.doc.source";`,
		`localStorage.getItem(sourceKey) === "on"`, `localStorage.setItem(sourceKey, "on")`, `localStorage.removeItem(sourceKey)`,
		`root.querySelectorAll(".doc-toggle[data-remember] > *")`,
		`e.target.closest(".doc-toggle[data-remember] > a")`,
		`location.replace(second.href + (/^#L\d+$/.test(location.hash) ? location.hash : ""))`,
		`if (!a || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;`, `else second.click()`,
		`const input = form.querySelector("input[type=search]");`)
	lacks(t, "app.js", js, `const input = form.querySelector("input");`)
	lacks(t, "app.js", js, "hand.doc.source${")
	i := strings.Index(js, "const sourceSaved")
	j := strings.Index(js, "const sourceView")
	if i < 0 || j < i || !strings.Contains(js[i:j], "try {") || !strings.Contains(js[i:j], "catch {") {
		t.Errorf("the saved view is read without try and catch")
	}
	k := strings.Index(js, `.closest(".doc-toggle[data-remember] > a")`)
	if k < 0 || !strings.Contains(js[k:k+500], "catch {") {
		t.Errorf("the saved view is written without try and catch")
	}
}
