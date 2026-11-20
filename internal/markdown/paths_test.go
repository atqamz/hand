package markdown_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/markdown"
)

func TestPathCode(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"/abs/file.md", true},
		{"/a", true},
		{"/", false},
		{"//x", false},
		{"/ x", false},
		{"/\tx", false},
		{`/\x`, false},
		{"~/notes.md", true},
		{"~/", false},
		{"~", false},
		{"~x/y", false},
		{`C:\x`, true},
		{"C:/x", true},
		{"c:/x", true},
		{`C:\`, false},
		{"C:/", false},
		{"C:x/y", false},
		{"1:/x", true},
		{"reports/one.md", true},
		{"./reports/one.md", true},
		{"reports/", true},
		{"a/b/c.txt", true},
		{"top.md", false},
		{"", false},
		{"reports/../top.md", false},
		{"../reports/one.md", false},
		{"reports/..", false},
		{"reports/..x/y", true},
		{"reports/one two.md", false},
		{"reports/one\ttwo.md", false},
		{"https://x/y", false},
		{"reports://x", false},
		{"/abs/file.md:12", true},
		{"reports/one.md#L3", true},
		{"reports/one.md:12", true},
		{"/abs/one two.md", true},
	} {
		if got := markdown.PathCode(c.in); got != c.want {
			t.Errorf("PathCode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPathAt(t *testing.T) {
	for _, c := range []struct {
		s    string
		i    int
		want int
	}{
		{"/abs/file.md", 0, 12},
		{"see /abs/file.md now", 4, 12},
		{"/abs/file.md,", 0, 12},
		{"/abs/file.md.", 0, 12},
		{"/abs/file.md)", 0, 12},
		{"/abs/file.md).", 0, 12},
		{"(/abs/file.md)", 1, 12},
		{"(/abs/file.md", 1, 12},
		{"/abs/(x)", 0, 8},
		{"/abs/file.md;", 0, 12},
		{"/abs/file.md!?", 0, 12},
		{"/abs/file.md:12", 0, 15},
		{"/abs/file.md#L3", 0, 15},
		{"/abs/file.md: next", 0, 12},
		{"/abs/file.md next", 0, 12},
		{"/abs/file.md\tnext", 0, 12},
		{"/abs/file.md\nnext", 0, 12},
		{"/abs/a<b", 0, 6},
		{`/abs/a"b`, 0, 6},
		{"/abs/a'b", 0, 6},
		{"/abs/a`b", 0, 6},
		{"~/notes.md", 0, 10},
		{`C:\x\y.txt`, 0, 10},
		{"C:/x/y.txt", 0, 10},
		{"in C:/x/y.txt.", 3, 10},
		{"reports/one.md", 0, 0},
		{"top.md", 0, 0},
		{"/", 0, 0},
		{"/ x", 0, 0},
		{"//x", 0, 0},
		{"a/abs/x", 1, 0},
		{"a/abs/x", 2, 0},
		{"x/abs", 1, 0},
		{`x\abs`, 1, 0},
		{"x.~/abs", 2, 0},
		{"x~/abs", 1, 0},
		{"x-/abs", 2, 0},
		{"x_/abs", 2, 0},
		{"xy:/abs", 3, 0},
		{"x:/abs", 2, 0},
		{"1/abs", 1, 0},
		{"é/abs", 2, 0},
		{" /abs", 1, 4},
		{"*/abs", 1, 4},
		{"(/abs", 1, 4},
		{"[/abs", 1, 4},
		{"|/abs", 1, 4},
		{",/abs", 1, 4},
		{"=/abs", 1, 4},
		{"/abs/日本語", 0, 14},
		{"/abs/日本語.", 0, 14},
		{"/abs", 5, 0},
		{"/abs", -1, 0},
		{"/abs", 4, 0},
		{"/abs", 9, 0},
		{"/.", 0, 0},
		{"/a.", 0, 2},
	} {
		if got := markdown.PathAt(c.s, c.i); got != c.want {
			t.Errorf("PathAt(%q, %d) = %d, want %d", c.s, c.i, got, c.want)
		}
	}
}

func candidates(text string) []string {
	var seen []string
	markdown.Render(text, func(p string) string {
		seen = append(seen, p)
		return "/doc/" + p
	})
	return seen
}

func TestRenderOffersCandidates(t *testing.T) {
	for _, c := range []struct {
		name string
		text string
		want []string
	}{
		{"code span relative", "`reports/one.md`", []string{"reports/one.md"}},
		{"code span absolute", "`/abs/one.md`", []string{"/abs/one.md"}},
		{"code span home", "`~/one.md`", []string{"~/one.md"}},
		{"code span drive backslash", "`C:\\x`", []string{`C:\x`}},
		{"code span drive slash", "`C:/x`", []string{"C:/x"}},
		{"code span suffix kept", "`reports/one.md:12` `reports/one.md#L3`", []string{"reports/one.md:12", "reports/one.md#L3"}},
		{"code span dot dot", "`reports/../top.md`", nil},
		{"code span space", "`reports/one two.md`", nil},
		{"code span tab", "`reports/one\ttwo.md`", nil},
		{"code span scheme", "`https://x/y` `a://b/c`", nil},
		{"code span no slash", "`top.md`", nil},
		{"code span leading slash", "`/ x` `//x`", nil},
		{"code span tilde not home", "`~x/y`", nil},
		{"code span keeps its own trailing punctuation", "`/abs/one.md.`", []string{"/abs/one.md."}},
		{"prose absolute", "see /abs/one.md now", []string{"/abs/one.md"}},
		{"prose relative is no candidate", "see reports/one.md now", nil},
		{"prose home", "see ~/one.md now", []string{"~/one.md"}},
		{"prose drive", `see C:\x\y.txt and C:/x/y.txt`, []string{`C:\x\y.txt`, "C:/x/y.txt"}},
		{"prose trailing comma", "/abs/one.md, next", []string{"/abs/one.md"}},
		{"prose trailing dot", "read /abs/one.md.", []string{"/abs/one.md"}},
		{"prose trailing paren", "(see /abs/one.md)", []string{"/abs/one.md"}},
		{"prose suffix kept", "/abs/one.md:12 and /abs/one.md#L3", []string{"/abs/one.md:12", "/abs/one.md#L3"}},
		{"prose space ends it", "/abs/one two.md", []string{"/abs/one"}},
		{"prose after word char", "a/abs/one.md x/abs", nil},
		{"prose after boundary punctuation", "x.~/abs xy:/abs x-/abs x_/abs", nil},
		{"prose after slash", "a//abs/one.md", nil},
		{"prose url", "https://x.test/abs/one.md", nil},
		{"bold", "**/abs/one.md**", []string{"/abs/one.md"}},
		{"italic star", "*/abs/one.md*", []string{"/abs/one.md"}},
		{"italic underscore", "_/abs/one.md_", []string{"/abs/one.md"}},
		{"strike", "~~/abs/one.md~~", []string{"/abs/one.md"}},
		{"bold code", "**`reports/one.md`**", []string{"reports/one.md"}},
		{"bold relative", "**reports/one.md**", nil},
		{"list", "- /abs/a.md\n- `reports/b.md`", []string{"/abs/a.md", "reports/b.md"}},
		{"ordered list", "1. /abs/a.md", []string{"/abs/a.md"}},
		{"task list", "- [ ] /abs/a.md", []string{"/abs/a.md"}},
		{"quote", "> /abs/a.md and `reports/b.md`", []string{"/abs/a.md", "reports/b.md"}},
		{"table", "| a | b |\n|---|---|\n| /abs/a.md | `reports/b.md` |", []string{"/abs/a.md", "reports/b.md"}},
		{"heading", "# /abs/a.md\n\n## `reports/b.md`", []string{"/abs/a.md", "reports/b.md"}},
		{"fenced", "```\n/abs/a.md\n`reports/b.md`\n```", nil},
		{"fenced with info", "```sh\ncat /abs/a.md\n```", nil},
		{"fenced then prose", "```\n/abs/a.md\n```\n\n/abs/b.md", []string{"/abs/b.md"}},
		{"unterminated fence", "```\n/abs/a.md", nil},
		{"markdown link text", "[/abs/a.md](https://x.test/y)", nil},
		{"unclosed code span stays prose", "`/abs/a.md", []string{"/abs/a.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := candidates(c.text); !slices.Equal(got, c.want) {
				t.Errorf("candidates(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func TestRenderOffersAtMostFiftyCandidates(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "`reports/%02d.md` /abs/%02d.md\n\n", i, i)
	}
	text := b.String()
	var seen []string
	out := string(markdown.Render(text, func(p string) string {
		seen = append(seen, p)
		return ""
	}))
	if len(seen) != 50 || seen[0] != "reports/00.md" || seen[1] != "/abs/00.md" || seen[49] != "/abs/24.md" {
		t.Errorf("failed candidates: %d offered, first %q, last %q; want 50 in document order, failures counted", len(seen), seen[0], seen[len(seen)-1])
	}
	if strings.Contains(out, `class="path"`) {
		t.Errorf("a refused candidate was linked: %s", out)
	}

	seen = nil
	out = string(markdown.Render("`top.md` `a b/c` not/a/candidate "+text, func(p string) string {
		seen = append(seen, p)
		return "/doc/" + p
	}))
	if n := strings.Count(out, `class="path"`); n != 50 || len(seen) != 50 {
		t.Errorf("%d links, %d offered; non-candidates must not use the cap", n, len(seen))
	}
	if strings.Contains(out, "/doc/reports/25.md") || strings.Contains(out, "/doc/%2Fabs%2F25.md") || strings.Contains(out, "/doc//abs/25.md") {
		t.Errorf("the 51st candidate was linked")
	}

	if len(candidates(text)) != 50 || len(candidates(text)) != 50 {
		t.Errorf("each render starts a new count of 50")
	}
}
