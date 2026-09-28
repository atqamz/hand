package markdown_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
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
		{"# not a heading", `<p># not a heading</p>`},
		{"", ``},
	} {
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
	"[a](https://b.io)[c](https://d.io)", "**a *b _c `d` e_ f* g**", "\x00<\x00script>", "&lt;script&gt;", "[`x`](https://a.io)",
}

func TestRenderNeverEmitsActiveMarkup(t *testing.T) {
	for _, in := range corpus {
		if msg := active(string(markdown.Render(in))); msg != "" {
			t.Errorf("Render(%q): %s", in, msg)
		}
	}
}

func FuzzRender(f *testing.F) {
	for _, in := range corpus {
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
	tagPattern  = regexp.MustCompile(`^<(/?)([a-z]+)((?: [a-z]+="[^"<>]*")*)>`)
	attrPattern = regexp.MustCompile(` ([a-z]+)="([^"]*)"`)
	allowed     = map[string]bool{"p": true, "br": true, "ul": true, "ol": true, "li": true, "pre": true, "code": true, "strong": true, "em": true, "a": true}
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
