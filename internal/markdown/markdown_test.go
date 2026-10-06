package markdown_test

import (
	"net/url"
	"regexp"
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
		{"[PR](https://github.com/a/b/pull/1)", `<p><a href="https://github.com/a/b/pull/1" rel="noopener noreferrer">PR</a></p>`},
		{"see https://x.io/a?b=1&c=2.", `<p>see <a href="https://x.io/a?b=1&amp;c=2" rel="noopener noreferrer">https://x.io/a?b=1&amp;c=2</a>.</p>`},
		{"snake_case_name and a_b", `<p>snake_case_name and a_b</p>`},
		{"`**not bold**`", `<p><code>**not bold**</code></p>`},
		{"#not a heading", `<p>#not a heading</p>`},
		{"", ``},
	} {
		if got := string(markdown.Render(c.in)); got != c.want {
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
		if got := string(markdown.Render(c.in)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderRefs(t *testing.T) {
	link := func(ref string) string { return `<a class="ref" href="/f1/ref/` + ref + `">` + ref + `</a>` }
	for _, c := range []struct{ in, want string }{
		{"see t12 and d3", `<p>see ` + link("t12") + ` and ` + link("d3") + `</p>`},
		{"`t12`", `<p><code>t12</code></p>`},
		{"[x](https://a.io/t12)", `<p><a href="https://a.io/t12" rel="noopener noreferrer">x</a></p>`},
		{"at12 t12x", `<p>at12 t12x</p>`},
		{"r7.", `<p>` + link("r7") + `.</p>`},
		{"**a5** done", `<p><strong>` + link("a5") + `</strong> done</p>`},
		{"- t1\n- t0", `<ul><li>` + link("t1") + `</li><li>t0</li></ul>`},
		{"see test_d3.go and t1_done and t3.micro", `<p>see test_d3.go and t1_done and t3.micro</p>`},
		{"t3. next", `<p>` + link("t3") + `. next</p>`},
	} {
		if got := string(markdown.RenderRefs(c.in, "/f1", nil)); got != c.want {
			t.Errorf("RenderRefs(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if got := string(markdown.Render("see t12")); got != `<p>see t12</p>` {
		t.Errorf("Render links refs: %s", got)
	}
	for _, in := range seeds() {
		if msg := active(string(markdown.RenderRefs(in+" t1 d2", "/f1", nil))); msg != "" {
			t.Errorf("RenderRefs(%q): %s", in, msg)
		}
	}
}

func TestRenderBlocks(t *testing.T) {
	for _, c := range blocks {
		if got := string(markdown.Render(c.in)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}

func TestRenderEscapesHTML(t *testing.T) {
	for _, in := range []string{"<script>alert(1)</script>", "<img src=x onerror=y>", "a & b > c"} {
		got := string(markdown.Render(in))
		if strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(got, "<p>"), "</p>"), "<>") {
			t.Errorf("Render(%q) = %s", in, got)
		}
	}
}

func TestRenderRefusesUnsafeLinks(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "JaVaScRiPt:alert(1)", "data:text/html,x", "vbscript:x", "//evil.io/x", "/relative", "mailto:a@b.c", "https:///nohost"} {
		if got := string(markdown.Render("[x](" + u + ")")); strings.Contains(got, "<a") {
			t.Errorf("unsafe link %q rendered %s", u, got)
		}
	}
}

func TestRenderAttributeInjection(t *testing.T) {
	for _, in := range []string{`[x](https://a.io/"onmouseover="y)`, `[x"](https://a.io)`, `https://a.io/"onmouseover="y`, "[x](https://a.io/'onfocus='y)"} {
		got := string(markdown.Render(in))
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
		{"[a](https://x.io", `<p>[a](<a href="https://x.io" rel="noopener noreferrer">https://x.io</a></p>`},
		{"```\nx\ny", "<pre><code>x\ny</code></pre>"},
	} {
		if got := string(markdown.Render(c.in)); got != c.want {
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

func TestRenderNeverEmitsActiveMarkup(t *testing.T) {
	for _, in := range seeds() {
		if msg := active(string(markdown.Render(in))); msg != "" {
			t.Errorf("Render(%q): %s", in, msg)
		}
	}
}

func seeds() []string {
	out := append([]string(nil), corpus...)
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
		if msg := active(string(markdown.Render(in))); msg != "" {
			t.Fatalf("Render(%q): %s", in, msg)
		}
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

func active(out string) string {
	for i := 0; i < len(out); i++ {
		if out[i] != '<' {
			continue
		}
		m := tagPattern.FindStringSubmatch(out[i:])
		if m == nil {
			return "a bare < that is not an allowed tag at " + out[i:min(len(out), i+20)]
		}
		if !allowed[m[2]] {
			return "element " + m[2]
		}
		for _, a := range attrPattern.FindAllStringSubmatch(m[3], -1) {
			if want, ok := fixed[m[2]+" "+a[1]]; ok {
				if a[2] != want {
					return "value " + a[2] + " for " + a[1] + " on " + m[2]
				}
				continue
			}
			if m[2] == "ol" && a[1] == "start" && startPattern.MatchString(a[2]) {
				continue
			}
			if m[2] == "a" && a[1] == "class" && a[2] == "ref" {
				continue
			}
			if m[2] == "a" && a[1] == "href" && strings.HasPrefix(a[2], "/f1/ref/") {
				continue
			}
			if (m[2] == "th" || m[2] == "td") && a[1] == "class" {
				if !aligns[a[2]] {
					return "class " + a[2] + " on " + m[2]
				}
				continue
			}
			switch {
			case m[2] != "a" || (a[1] != "href" && a[1] != "rel"):
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
	for _, unit := range []string{"*a ", "_a ", "**a ", "`a ", "[a ", "~~a ", "> ", "# ", "> a\n", "| a | b |\n", "| a |\n|---|\n", "|"} {
		in := strings.Repeat(unit, 200<<10/len(unit))
		start := time.Now()
		markdown.Render(in)
		if d := time.Since(start); d > time.Second {
			t.Errorf("Render of %d bytes of %q took %v", len(in), unit, d)
		}
	}
}

func TestRenderRefsAddsTitles(t *testing.T) {
	title := func(ref string) string { return map[string]string{"t1": `Fix "login"`}[ref] }
	got := string(markdown.RenderRefs("see t1 and a2", "/f1", title))
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
	} {
		if got := string(markdown.Render(c.in)); got != c.want {
			t.Errorf("Render(%q)\n got %s\nwant %s", c.in, got, c.want)
		}
	}
}
