package markdown_test

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/markdown"
)

func TestRenderSubset(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"**a** *b* _c_ `d`", `<p><strong>a</strong> <em>b</em> <em>c</em> <code>d</code></p>`},
		{"x\ny", `<p>x<br>y</p>`},
		{"x\n\ny", `<p>x</p><p>y</p>`},
		{"x\r\n\r\ny", `<p>x</p><p>y</p>`},
		{"- a\n- b", `<ul><li>a</li><li>b</li></ul>`},
		{"* a\n* **b**", `<ul><li>a</li><li><strong>b</strong></li></ul>`},
		{"1. a\n2. b", `<ol><li>a</li><li>b</li></ol>`},
		{"intro\n- a\n\nafter", `<p>intro</p><ul><li>a</li></ul><p>after</p>`},
		{"```go\n<b>\n```", `<pre><code>&lt;b&gt;</code></pre>`},
		{"```\na\n\nb\n```\nnext", `<pre><code>a` + "\n\n" + `b</code></pre><p>next</p>`},
		{"[PR](https://github.com/a/b/pull/1)", `<p><a href="https://github.com/a/b/pull/1" target="_blank" rel="noopener noreferrer">PR</a></p>`},
		{"see https://x.io/a?b=1&c=2.", `<p>see <a href="https://x.io/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer">https://x.io/a?b=1&amp;c=2</a>.</p>`},
		{"snake_case_name and a_b", `<p>snake_case_name and a_b</p>`},
		{"`**not bold**`", `<p><code>**not bold**</code></p>`},
		{"#not a heading", `<p>#not a heading</p>`},
		{"", ``},
	} {
		if got := string(markdown.Render(c.in, nil)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

var blocks = []struct{ in, want string }{
	{"## Sources\ntext", `<h3>Sources</h3><p>text</p>`},
	{"# A", `<h3>A</h3>`},
	{"### B", `<h4>B</h4>`},
	{"###### C", `<h4>C</h4>`},
	{"#tag", `<p>#tag</p>`},
	{"## **Bold** head", `<h3><strong>Bold</strong> head</h3>`},
	{"a\n---\nb", `<p>a</p><hr><p>b</p>`},
	{"***", `<hr>`},
	{"_ _ _", `<hr>`},
	{"--", `<p>--</p>`},
	{"> quoted\n> more\n\nafter", `<blockquote><p>quoted<br>more</p></blockquote><p>after</p>`},
	{"> - a\n> - b", `<blockquote><ul><li>a</li><li>b</li></ul></blockquote>`},
	{"> > deep", `<blockquote><p>&gt; deep</p></blockquote>`},
	{"~~gone~~ kept", `<p><del>gone</del> kept</p>`},
	{"~~open", `<p>~~open</p>`},
}

const wrap = `<div class="table" role="region" aria-label="Table" tabindex="0"><table>`

var tables = []struct{ in, want string }{
	{"| a | b |\n|---|---:|\n| x | 1 |", wrap + `<thead><tr><th>a</th><th class="r">b</th></tr></thead><tbody><tr><td>x</td><td class="r">1</td></tr></tbody></table></div>`},
	{"| day | online |\n|---|---|\n| Mon | 314 |\n| Tue | 1.2k–3k |\n| Wed | 12% → 46% |", wrap + `<thead><tr><th>day</th><th class="r">online</th></tr></thead><tbody><tr><td>Mon</td><td class="r">314</td></tr><tr><td>Tue</td><td class="r">1.2k–3k</td></tr><tr><td>Wed</td><td class="r">12% → 46%</td></tr></tbody></table></div>`},
	{"| n |\n|---|\n| 12 |\n| n/a |", wrap + `<thead><tr><th>n</th></tr></thead><tbody><tr><td>12</td></tr><tr><td>n/a</td></tr></tbody></table></div>`},
	{"| a | b |\n|:---|:-:|\n| 1 | 2 |", wrap + `<thead><tr><th class="l">a</th><th class="c">b</th></tr></thead><tbody><tr><td class="l">1</td><td class="c">2</td></tr></tbody></table></div>`},
	{"a | b\n--- | ---\nx | y", wrap + `<thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>x</td><td>y</td></tr></tbody></table></div>`},
	{"| a \\| b |\n|---|\n| c |", wrap + `<thead><tr><th>a | b</th></tr></thead><tbody><tr><td>c</td></tr></tbody></table></div>`},
	{"| a | b |\n|---|---|\n| x |\n| p | q | r |", wrap + `<thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>x</td><td></td></tr><tr><td>p</td><td>q</td></tr></tbody></table></div>`},
	{"| | **28 Sep** |\n|---|---|\n| online | **291** |", wrap + `<thead><tr><th></th><th class="r"><strong>28 Sep</strong></th></tr></thead><tbody><tr><td>online</td><td class="r"><strong>291</strong></td></tr></tbody></table></div>`},
	{"intro\n| a |\n|---|\n| b |", `<p>intro</p>` + wrap + `<thead><tr><th>a</th></tr></thead><tbody><tr><td>b</td></tr></tbody></table></div>`},
	{"| a |\n|---|\n| b |\nafter", wrap + `<thead><tr><th>a</th></tr></thead><tbody><tr><td>b</td></tr></tbody></table></div><p>after</p>`},
	{"a | b\nc | d", `<p>a | b<br>c | d</p>`},
	{"| a | b |\n|---|---|---|\n| x | y |", `<p>| a | b |<br>|---|---|---|<br>| x | y |</p>`},
	{"```\n| a |\n|---|\n```", "<pre><code>| a |\n|---|</code></pre>"},
}

func TestRenderTables(t *testing.T) {
	for _, c := range tables {
		if got := string(markdown.Render(c.in, nil)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderRefs(t *testing.T) {
	link := func(ref string) string { return `<a class="ref" href="/f1/ref/` + ref + `">` + ref + `</a>` }
	for _, c := range []struct{ in, want string }{
		{"see t12 and d3", `<p>see ` + link("t12") + ` and ` + link("d3") + `</p>`},
		{"`t12`", `<p><code>t12</code></p>`},
		{"[x](https://a.io/t12)", `<p><a href="https://a.io/t12" target="_blank" rel="noopener noreferrer">x</a></p>`},
		{"at12 t12x", `<p>at12 t12x</p>`},
		{"r7.", `<p>` + link("r7") + `.</p>`},
		{"**a5** done", `<p><strong>` + link("a5") + `</strong> done</p>`},
		{"- t1\n- t0", `<ul><li>` + link("t1") + `</li><li>t0</li></ul>`},
		{"see test_d3.go and t1_done and t3.micro", `<p>see test_d3.go and t1_done and t3.micro</p>`},
		{"t3. next", `<p>` + link("t3") + `. next</p>`},
		{`C:\x\t12\a.md and /x/t3/a.md`, `<p>C:\x\t12\a.md and /x/t3/a.md</p>`},
	} {
		if got := string(markdown.RenderRefs(c.in, "/f1", nil, nil)); got != c.want {
			t.Errorf("RenderRefs(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := string(markdown.Render("see t12", nil)); got != `<p>see t12</p>` {
		t.Errorf("Render links refs: %s", got)
	}
	for _, in := range seeds() {
		if msg := active(string(markdown.RenderRefs(in+" t1 d2", "/f1", nil, nil))); msg != "" {
			t.Errorf("RenderRefs(%q): %s", in, msg)
		}
	}
}

func TestRenderBlocks(t *testing.T) {
	for _, c := range blocks {
		if got := string(markdown.Render(c.in, nil)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderEscapesHTML(t *testing.T) {
	for _, in := range []string{"<script>alert(1)</script>", "<img src=x onerror=y>", "a & b > c"} {
		got := string(markdown.Render(in, nil))
		if strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(got, "<p>"), "</p>"), "<>") {
			t.Errorf("Render(%q) = %s", in, got)
		}
	}
}

func TestRenderRefusesUnsafeLinks(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", "data:text/html,x", "vbscript:x", "//evil.io/x", "/relative", "mailto:a@b.c", "https:///nohost"} {
		if got := string(markdown.Render("[x]("+u+")", nil)); strings.Contains(got, "<a") {
			t.Errorf("unsafe link %q rendered %s", u, got)
		}
	}
}

func TestRenderAttributeInjection(t *testing.T) {
	for _, in := range []string{`[x](https://a.io/"onmouseover="y)`, `[x"](https://a.io)`, `https://a.io/"onmouseover="y`, "[x](https://a.io/'onfocus='y)"} {
		got := string(markdown.Render(in, nil))
		if msg := active(got); msg != "" {
			t.Errorf("Render(%q) = %s: %s", in, got, msg)
		}
	}
}

func TestRenderUnclosedMarkers(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"**a", `<p>**a</p>`},
		{"*a", `<p>*a</p>`},
		{"`a", "<p>`a</p>"},
		{"[a](", `<p>[a](</p>`},
		{"[a](https://x.io", `<p>[a](<a href="https://x.io" target="_blank" rel="noopener noreferrer">https://x.io</a></p>`},
		{"```\nx\ny", "<pre><code>x\ny</code></pre>"},
	} {
		if got := string(markdown.Render(c.in, nil)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

var corpus = []string{
	"<script>alert(1)</script>", "<img src=x onerror=y>", "[x](javascript:alert(1))", "[x](JaVaScRiPt:alert(1))",
	"[x](data:text/html,<b>)", `[x](https://a.io/"onmouseover="y)`, `[x"](https://a.io)`, "**[x](javascript:y)**",
	"*<b>*", "`<b>`", "```\n<script>\n```", "- <i>\n- [a](vbscript:x)", "1. **x\n2. _y", "https://a.io/<b>", `https://a.io/"x`,
	"3. a\n   - <b>\n\n4. [x](javascript:y)", "[a](https://b.io)[c](https://d.io)", "**a *b _c `d` e_ f* g**", "\x00<\x00script>", "&lt;script&gt;", "[`x`](https://a.io)",
}

var docCorpus = []string{
	"[x](api.md)", "[x](../t286/notes.md)", "[x](sub/a b.md)", "[x](a.md#h)", `[x](a.md"onmouseover="y)`, "[x](../../.ssh/id)",
	"[x]( javascript:alert(1))", "[x](java\tscript:alert(1))", "[x](  //evil.io/x)", `[x](\\evil.io\x)`, `[x](/\evil.io)`, "[x](#top)",
	`![a"b](img/x.png)`, "![x](javascript:alert(1))", "![x](//evil.io/x.png)", "![x](https://evil.io/x.png)", "![x](x.svg)", "![](x.png)",
	"- [ ] todo <b>", "- [x] done", "1. [X] [x](a.md)", "| a |\n|---|\n| ![x](y.png) |", "![x", "![x](", "![[x](a.md)](b.md)",
}

var pathCorpus = []string{
	"/fleet/a.md", "see /fleet/a.md.", "`/fleet/a b.md`", `/fleet/"onmouseover="x.md`, "`/fleet/\"><script>`", "/fleet/<b>.md", `C:\fleet\a.md`, "~/a.md",
	"[/fleet/a.md](https://a.io)", "[`/fleet/a.md`](https://a.io)", "**/fleet/a.md**", "| /fleet/a.md |\n|---|\n| `/x` |", "- t1 /fleet/t1/a.md", "/fleet/a.md:12",
}

func stub(p string) string { return "/f1/doc/" + p }

func deny(string) string { return "" }

func safeWithPaths(t *testing.T, in string, fail func(string, ...any)) {
	t.Helper()
	plain := string(markdown.Render(in, nil))
	if msg := active(plain); msg != "" {
		fail("Render(%q): %s", in, msg)
	}
	if got := string(markdown.Render(in, deny)); got != plain {
		fail("Render(%q) with a hook that links nothing = %s, want %s", in, got, plain)
	}
	if msg := active(string(markdown.Render(in, stub))); msg != "" {
		fail("Render(%q) with paths: %s", in, msg)
	}
	if msg := active(string(markdown.RenderRefs(in, "/f1", nil, stub))); msg != "" {
		fail("RenderRefs(%q) with paths: %s", in, msg)
	}
	if msg := activeDoc(string(markdown.RenderDoc(in, "/f1", nil, nil))); msg != "" {
		fail("RenderDoc(%q): %s", in, msg)
	}
	if msg := activeDoc(string(markdown.RenderDoc(in, "/f1", nil, stub))); msg != "" {
		fail("RenderDoc(%q) with paths: %s", in, msg)
	}
}

func TestRenderNeverEmitsActiveMarkup(t *testing.T) {
	for _, in := range seeds() {
		safeWithPaths(t, in, t.Errorf)
	}
}

func seeds() []string {
	out := append(append(append([]string(nil), corpus...), docCorpus...), pathCorpus...)
	for _, b := range append(blocks, tables...) {
		out = append(out, b.in)
	}
	return out
}

func FuzzRender(f *testing.F) {
	for _, in := range seeds() {
		f.Add(in)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if !utf8.ValidString(in) {
			return
		}
		safeWithPaths(t, in, t.Fatalf)
	})
}

var (
	tagPattern   = regexp.MustCompile(`^<(/?)([a-z][a-z0-9]*)((?: [a-z-]+="[^"<>]*")*)>`)
	attrPattern  = regexp.MustCompile(` ([a-z-]+)="([^"]*)"`)
	allowed      = map[string]bool{"p": true, "br": true, "ul": true, "ol": true, "li": true, "pre": true, "code": true, "strong": true, "em": true, "a": true, "h3": true, "h4": true, "hr": true, "blockquote": true, "del": true, "div": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}
	startPattern = regexp.MustCompile(`^[0-9]+$`)
	fixed        = map[string]string{"div class": "table", "div role": "region", "div aria-label": "Table", "div tabindex": "0"}
	aligns       = map[string]bool{"l": true, "r": true, "c": true}
)

func active(out string) string { return markup(out, false) }

func activeDoc(out string) string { return markup(out, true) }

func relativeRef(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && raw != "" && !strings.ContainsAny(raw[:1], "/# ") && u.Scheme == "" && u.Host == "" && !strings.Contains(raw, `\`)
}

func markup(out string, doc bool) string {
	for i := 0; i < len(out); i++ {
		if out[i] != '<' {
			continue
		}
		m := tagPattern.FindStringSubmatch(out[i:])
		if m == nil {
			return "a bare < that is not an allowed tag at " + out[i:min(len(out), i+20)]
		}
		if !allowed[m[2]] && !(doc && (m[2] == "img" || m[2] == "span")) {
			return "element " + m[2]
		}
		for _, a := range attrPattern.FindAllStringSubmatch(m[3], -1) {
			if doc {
				switch v := html.UnescapeString(a[2]); m[2] + " " + a[1] {
				case "img src", "a href":
					if relativeRef(v) {
						continue
					}
				case "img alt":
					continue
				case "img loading":
					if v == "lazy" {
						continue
					}
				case "li class":
					if v == "task" || v == "task done" {
						continue
					}
				case "span class":
					if v == "sr" {
						continue
					}
				}
			}
			if want, ok := fixed[m[2]+" "+a[1]]; ok {
				if a[2] != want {
					return "value " + a[2] + " for " + a[1] + " on " + m[2]
				}
				continue
			}
			if m[2] == "ol" && a[1] == "start" && startPattern.MatchString(a[2]) {
				continue
			}
			if m[2] == "a" && a[1] == "class" && (a[2] == "ref" || a[2] == "path") {
				continue
			}
			if m[2] == "a" && a[1] == "href" && (strings.HasPrefix(a[2], "/f1/ref/") || strings.HasPrefix(a[2], "/f1/doc/")) {
				continue
			}
			if (m[2] == "th" || m[2] == "td") && a[1] == "class" {
				if !aligns[a[2]] {
					return "class " + a[2] + " on " + m[2]
				}
				continue
			}
			switch {
			case m[2] == "a" && a[1] == "target":
				if a[2] != "_blank" {
					return "value " + a[2] + " for target on a"
				}
			case m[2] != "a" || (a[1] != "href" && a[1] != "rel" && a[1] != "target"):
				return "attribute " + a[1] + " on " + m[2]
			case a[1] == "href":
				u, err := url.Parse(strings.ReplaceAll(a[2], "&amp;", "&"))
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return "href " + a[2]
				}
			}
		}
	}
	return ""
}

func TestRenderIsLinearOnUnclosedMarkers(t *testing.T) {
	for _, unit := range []string{"*a ", "_a ", "**a ", "`a ", "[a ", "~~a ", "> ", "# ", "> a\n", "| a | b |\n", "| a |\n|---|\n", "|", "![a ", "![a](b ", "[a](javascript:b) ", "- [ ] a\n", "/(", "/a ", "`/a` ", "C:\\a "} {
		in := strings.Repeat(unit, 200<<10/len(unit))
		start := time.Now()
		markdown.Render(in, nil)
		markdown.RenderDoc(in, "/f1", nil, stub)
		markdown.RenderRefs(in, "/f1", nil, deny)
		if d := time.Since(start); d > time.Second {
			t.Errorf("Render and RenderDoc of %d bytes of %q took %v", len(in), unit, d)
		}
	}
}

func TestRenderRefsAddsTitles(t *testing.T) {
	title := func(ref string) string { return map[string]string{"t1": `Fix "login"`}[ref] }
	got := string(markdown.RenderRefs("see t1 and a2", "/f1", title, nil))
	for _, want := range []string{`<a class="ref" href="/f1/ref/t1" title="Fix &#34;login&#34;">t1</a>`, `<a class="ref" href="/f1/ref/a2">a2</a>`} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderRefs = %s, want %s", got, want)
		}
	}
}

func TestRefsLinksOneLineWithoutBlocks(t *testing.T) {
	got := string(markdown.Refs("wake: attempt.quiet a7: <turn> ended", "", nil))
	if got != `wake: attempt.quiet <a class="ref" href="/ref/a7">a7</a>: &lt;turn&gt; ended` {
		t.Fatalf("Refs = %s", got)
	}
}

func TestRenderNumberedListsKeepTheirNumbers(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1. a\n- x\n- y\n\n2. b\n- z", `<ol><li>a</li></ol><ul><li>x</li><li>y</li></ul><ol start="2"><li>b</li></ol><ul><li>z</li></ul>`},
		{"1. **a**\n   - x\n   - y\n\n2. **b**\n   - z\n\n3. c", `<ol><li><strong>a</strong><ul><li>x</li><li>y</li></ul></li><li><strong>b</strong><ul><li>z</li></ul></li><li>c</li></ol>`},
		{"1. a\n  - x\n2. b", `<ol><li>a<ul><li>x</li></ul></li><li>b</li></ol>`},
		{"1. a\n    note\n2. b", `<ol><li>a<p>note</p></li><li>b</li></ol>`},
		{"1. a\n\n2. b", `<ol><li>a</li><li>b</li></ol>`},
		{"- a\n\n- b", `<ul><li>a</li><li>b</li></ul>`},
		{"3. a\n4. b", `<ol start="3"><li>a</li><li>b</li></ol>`},
		{"1. a\n\ntext\n\n2. b", `<ol><li>a</li></ol><p>text</p><ol start="2"><li>b</li></ol>`},
		{"1. a\n   1. x\n   2. y\n2. b", `<ol><li>a<ol><li>x</li><li>y</li></ol></li><li>b</li></ol>`},
		{"0. a\n1. b", `<ol start="0"><li>a</li><li>b</li></ol>`},
		{"2147483647. a", `<ol start="2147483647"><li>a</li></ol>`},
		{"2147483648. a", `<ol><li>a</li></ol>`},
		{"99999999999999999999. a\n2. b", `<ol><li>a</li><li>b</li></ol>`},
		{"1. a\n   ```\n   <x>\n\n   y\n   ```\n2. b", `<ol><li>a<pre><code>&lt;x&gt;` + "\n\n" + `y</code></pre></li><li>b</li></ol>`},
	} {
		if got := string(markdown.Render(c.in, nil)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderFourNumberedItemsWithBulletsStayOneList(t *testing.T) {
	var in, want strings.Builder
	want.WriteString("<ol>")
	for n := 1; n <= 4; n++ {
		if n > 1 {
			in.WriteString("\n\n")
		}
		fmt.Fprintf(&in, "%d. **Step %d**\n   - first note %d\n   - second note %d\n   - third note %d", n, n, n, n, n)
		fmt.Fprintf(&want, "<li><strong>Step %d</strong><ul><li>first note %d</li><li>second note %d</li><li>third note %d</li></ul></li>", n, n, n, n)
	}
	want.WriteString("</ol>")
	if got := string(markdown.Render(in.String(), nil)); got != want.String() {
		t.Errorf("Render(%q)\n got %s\nwant %s", in.String(), got, want.String())
	}
}

func TestRenderCapsListNestingKeepingText(t *testing.T) {
	var in strings.Builder
	for n := 0; n < 600; n++ {
		fmt.Fprintf(&in, "%s1. item%d\n", strings.Repeat(" ", n), n)
	}
	got := string(markdown.Render(in.String(), nil))
	if n := strings.Count(got, "<ol>"); n != 6 {
		t.Errorf("got %d nested lists, want 6", n)
	}
	for n := 0; n < 600; n++ {
		if !strings.Contains(got, fmt.Sprintf("item%d<", n)) {
			t.Fatalf("item%d missing from %.200s", n, got)
		}
	}
	if msg := active(got); msg != "" {
		t.Error(msg)
	}
}

func TestDocumentModeKeepsRelativeLinks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"[API notes](api.md)", `<p><a href="api.md">API notes</a></p>`},
		{"[notes](../t286/notes.md)", `<p><a href="../t286/notes.md">notes</a></p>`},
		{"[x](sub/a b.md)", `<p><a href="sub/a b.md">x</a></p>`},
		{"[x](a.md#h)", `<p><a href="a.md#h">x</a></p>`},
		{"[**b**](shots/)", `<p><a href="shots/"><strong>b</strong></a></p>`},
		{`[x](a.md"onmouseover="y)`, `<p><a href="a.md&#34;onmouseover=&#34;y">x</a></p>`},
		{"[PR](https://github.com/a/b/pull/1)", `<p><a href="https://github.com/a/b/pull/1" target="_blank" rel="noopener noreferrer">PR</a></p>`},
		{"see t12", `<p>see <a class="ref" href="/f1/ref/t12">t12</a></p>`},
		{"| doc |\n|---|\n| [a](a.md) |", wrap + `<thead><tr><th>doc</th></tr></thead><tbody><tr><td><a href="a.md">a</a></td></tr></tbody></table></div>`},
	} {
		if got := string(markdown.RenderDoc(c.in, "/f1", nil, nil)); got != c.want {
			t.Errorf("RenderDoc(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := string(markdown.Render("[API notes](api.md)", nil)); got != `<p>[API notes](api.md)</p>` {
		t.Errorf("Render keeps a relative link: %s", got)
	}
}

func TestDocumentModeRefusesSchemesHostsAndBackslashes(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", " javascript:alert(1)", "java\tscript:alert(1)", "data:text/html,x", "vbscript:x", "mailto:a@b.c", "c:/x",
		"//evil.io/x", "  //evil.io/x", "https:///nohost", `\\evil.io\x`, `/\evil.io`, `a\..\..\x`, `C:\x`, "/abs/path", "#top", ""} {
		for _, in := range []string{"[x](" + u + ")", "![x](" + u + ")"} {
			if got := string(markdown.RenderDoc(in, "/f1", nil, nil)); strings.Contains(got, "<a") || strings.Contains(got, "<img") {
				t.Errorf("RenderDoc(%q) = %s", in, got)
			}
		}
	}
}

func TestDocumentModeShowsRelativeImages(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"![The flow](images/flow.png)", `<p><img src="images/flow.png" alt="The flow" loading="lazy"></p>`},
		{`![a"b<c>](x.png)`, `<p><img src="x.png" alt="a&#34;b&lt;c&gt;" loading="lazy"></p>`},
		{"![](x.png) after", `<p><img src="x.png" alt="" loading="lazy"> after</p>`},
		{"![x](https://example.com/x.png)", `<p>!<a href="https://example.com/x.png" target="_blank" rel="noopener noreferrer">x</a></p>`},
		{"![x](data:image/png;base64,AAAA)", `<p>![x](data:image/png;base64,AAAA)</p>`},
		{"![x", `<p>![x</p>`},
	} {
		if got := string(markdown.RenderDoc(c.in, "/f1", nil, nil)); got != c.want {
			t.Errorf("RenderDoc(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := string(markdown.Render("![x](x.png)", nil)); got != `<p>![x](x.png)</p>` {
		t.Errorf("Render shows an image: %s", got)
	}
}

func TestTaskListItems(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"- [ ] todo <b>", `<ul><li class="task"><span class="sr">to do: </span>todo &lt;b&gt;</li></ul>`},
		{"- [x] done\n- [X] **also**", `<ul><li class="task done"><span class="sr">done: </span>done</li><li class="task done"><span class="sr">done: </span><strong>also</strong></li></ul>`},
		{"1. [ ] first\n   - [x] nested", `<ol><li class="task"><span class="sr">to do: </span>first<ul><li class="task done"><span class="sr">done: </span>nested</li></ul></li></ol>`},
		{"- [y] no\n- [ ]\n- [x]done", `<ul><li>[y] no</li><li>[ ]</li><li>[x]done</li></ul>`},
	} {
		if got := string(markdown.RenderDoc(c.in, "/f1", nil, nil)); got != c.want {
			t.Errorf("RenderDoc(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := string(markdown.Render("- [ ] todo", nil)); got != `<ul><li>[ ] todo</li></ul>` {
		t.Errorf("Render makes a task item: %s", got)
	}
}

func hook(allow ...string) (func(string) string, *[]string) {
	var calls []string
	return func(p string) string {
		calls = append(calls, p)
		for _, a := range allow {
			if p == a {
				return "/f1/doc/" + url.PathEscape(p)
			}
		}
		return ""
	}, &calls
}

func path(p, text string) string {
	return `<a class="path" href="/f1/doc/` + html.EscapeString(url.PathEscape(p)) + `" target="_blank" rel="noopener">` + text + `</a>`
}

func TestRenderLinksThePathsTheHookAllows(t *testing.T) {
	a, b := "/fleet/a.md", "/fleet/shots/"
	for _, c := range []struct{ in, want string }{
		{"see /fleet/a.md.", `<p>see ` + path(a, a) + `.</p>`},
		{"(/fleet/a.md), /fleet/a.md: and /fleet/a.md; ok /fleet/a.md!", `<p>(` + path(a, a) + `), ` + path(a, a) + `: and ` + path(a, a) + `; ok ` + path(a, a) + `!</p>`},
		{"the shots are in /fleet/shots/.", `<p>the shots are in ` + path(b, b) + `.</p>`},
		{"/fleet/a.md /fleet/shots/\n/fleet/a.md", `<p>` + path(a, a) + ` ` + path(b, b) + `<br>` + path(a, a) + `</p>`},
		{"`/fleet/a.md`", `<p>` + path(a, `<code>/fleet/a.md</code>`) + `</p>`},
		{"**/fleet/a.md** and *see /fleet/a.md*", `<p><strong>` + path(a, a) + `</strong> and <em>see ` + path(a, a) + `</em></p>`},
		{"## /fleet/a.md\n- /fleet/a.md\n\n> /fleet/a.md", `<h3>` + path(a, a) + `</h3><ul><li>` + path(a, a) + `</li></ul><blockquote><p>` + path(a, a) + `</p></blockquote>`},
		{"| doc |\n|---|\n| /fleet/a.md |", wrap + `<thead><tr><th>doc</th></tr></thead><tbody><tr><td>` + path(a, a) + `</td></tr></tbody></table></div>`},
		{"/fleet/a.md:12 and `/fleet/a.md:12`", `<p>/fleet/a.md:12 and <code>/fleet/a.md:12</code></p>`},
		{"/fleet/b.md and `/fleet/a.md and more`", `<p>/fleet/b.md and <code>/fleet/a.md and more</code></p>`},
		{"```\n/fleet/a.md\n```", `<pre><code>/fleet/a.md</code></pre>`},
		{"[/fleet/a.md](https://a.io) [`/fleet/a.md`](https://a.io)", `<p><a href="https://a.io" target="_blank" rel="noopener noreferrer">/fleet/a.md</a> <a href="https://a.io" target="_blank" rel="noopener noreferrer"><code>/fleet/a.md</code></a></p>`},
	} {
		local, _ := hook(a, b)
		if got := string(markdown.Render(c.in, local)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderPassesOnlyAbsolutePathsToTheHook(t *testing.T) {
	local, calls := hook()
	markdown.Render("a/fleet/x ./x ../x //host/x / and/or 1/2 https://a.io/fleet/x C:x c:\\ \\\\host\\share ~user/x ~ /\tx t1/x_/y\n```\n/fleet/x\n```", local)
	if len(*calls) != 0 {
		t.Fatalf("the hook saw %q", *calls)
	}
	markdown.Render("see /fleet/a.md:12, `/fleet/a b.md`, (C:\\fleet\\a.md) c:/fleet/a.md and ~/fleet/a.md.", local)
	if want := []string{"/fleet/a.md:12", "/fleet/a b.md", `C:\fleet\a.md`, "c:/fleet/a.md", "~/fleet/a.md"}; !slices.Equal(*calls, want) {
		t.Fatalf("the hook saw %q, want %q", *calls, want)
	}
}

func TestRenderLinksSpacesAndUnicodeInPaths(t *testing.T) {
	spaced, accented, win := "/fleet/résumé notes.md", "/fleet/naïve-日本.md", `C:\fleet\a b.md`
	local, _ := hook(spaced, accented, win, "/fleet/résumé")
	for _, c := range []struct{ in, want string }{
		{"`/fleet/résumé notes.md`", `<p>` + path(spaced, "<code>"+spaced+"</code>") + `</p>`},
		{"see /fleet/naïve-日本.md.", `<p>see ` + path(accented, accented) + `.</p>`},
		{"`C:\\fleet\\a b.md`", `<p>` + path(win, "<code>"+html.EscapeString(win)+"</code>") + `</p>`},
	} {
		if got := string(markdown.Render(c.in, local)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderRefsAndPathsTogether(t *testing.T) {
	local, _ := hook("/fleet/reports/t1/a.md")
	got := string(markdown.RenderRefs("t1 wrote /fleet/reports/t1/a.md and /fleet/reports/t2/b.md", "/f1", nil, local))
	want := `<p><a class="ref" href="/f1/ref/t1">t1</a> wrote ` + path("/fleet/reports/t1/a.md", "/fleet/reports/t1/a.md") + ` and /fleet/reports/t2/b.md</p>`
	if got != want {
		t.Fatalf("RenderRefs\n got %s\nwant %s", got, want)
	}
}

func TestRenderChecksAtMostFiftyPathsAMessage(t *testing.T) {
	var in strings.Builder
	var allow []string
	for n := range 60 {
		p := fmt.Sprintf("/fleet/p%d.md", n)
		allow = append(allow, p)
		if n%2 == 0 {
			p = "`" + p + "`"
		}
		in.WriteString(p + "\n")
	}
	local, calls := hook(allow...)
	got := string(markdown.Render(in.String(), local))
	if len(*calls) != 50 || strings.Count(got, `<a class="path"`) != 50 {
		t.Fatalf("checked %d paths and linked %d, want 50 of each", len(*calls), strings.Count(got, `<a class="path"`))
	}
	if !strings.Contains(got, "<br>/fleet/p59.md</p>") {
		t.Fatalf("a path past the cap is not text: %s", got)
	}
	markdown.Render(in.String(), local)
	if len(*calls) != 100 {
		t.Fatalf("the cap is not per message: %d checks", len(*calls))
	}
}
