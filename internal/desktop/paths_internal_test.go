package desktop

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func known(set ...string) func(string) string {
	return func(c string) string {
		if slices.Contains(set, c) {
			return "rel/" + c
		}
		return ""
	}
}

func boardCandidates(text string) []string {
	var seen []string
	markdown.Render(text, func(p string) string {
		seen = append(seen, p)
		return ""
	})
	return seen
}

func candidatesOf(src string) []string {
	_, cands := linkRefs("f1", src, func(string) string { return "" })
	return cands
}

func TestPathCandidatesAgreeWithTheBoard(t *testing.T) {
	for _, src := range []string{
		"read `reports/a.md` and `/abs/b.md` and `~/c.md`",
		"see /abs/one.md, then /abs/two.md.",
		"(/abs/one.md) [/abs/two.md] x/abs/three.md",
		"**/abs/one.md** *`reports/a.md`* _/abs/two.md_ ~~/abs/three.md~~",
		"**bold**/abs/one.md and `x`/abs/two.md",
		"a~~b~~/abs/one.md ~~b~~/abs/two.md a*b*/abs/three.md",
		"snake_/abs/one.md foo.~/abs/two.md a-/abs/three.md",
		"- /abs/a.md\n- `reports/b.md`\n\n> /abs/c.md\n\n| a | b |\n|---|---|\n| /abs/d.md | `reports/e.md` |",
		"# /abs/head.md\n\n```\n/abs/fenced.md\n```\n\n/abs/after.md",
		"[/abs/linked.md](https://example.test) /abs/plain.md",
		"` reports/a.md ` `reports/c.md`",
		"`reports/one two.md` `top.md` `reports/../x.md` `https://x/y`",
		"/abs/file.md:12 /abs/file.md#L3 C:\\x\\y.txt C:/x/y.txt",
		"https://example.test/abs/one.md /abs/two.md",
		"`reports/a.md.` `reports/b.md,`",
		"/a/\u65e5\u672c\u8a9e.md and `reports/\u65e5\u672c.md`",
	} {
		t.Run(src, func(t *testing.T) {
			if got, want := candidatesOf(src), boardCandidates(src); !slices.Equal(got, want) {
				t.Fatalf("desktop %q\n board %q", got, want)
			}
		})
	}
}

func TestACodeSpanKeepsItsRawText(t *testing.T) {
	for _, src := range []string{"` reports/a.md `", "``reports/a.md``", "` reports/a.md`", "`reports/a.md `"} {
		if got := candidatesOf(src); len(got) != 0 {
			t.Errorf("%q offered %q", src, got)
		}
	}
	if got := candidatesOf("`reports/a.md`"); !slices.Equal(got, []string{"reports/a.md"}) {
		t.Errorf("got %q", got)
	}
}

func TestACandidateTheBoardWouldRefuseIsNeverSent(t *testing.T) {
	long := "reports/" + strings.Repeat("a", pathValueMax)
	ok := "reports/" + strings.Repeat("a", pathValueMax-len("reports/"))
	for name, c := range map[string]string{
		"NUL": "reports/a\x00b", "CR": "reports/a\rb", "VT": "reports/a\vb", "FF": "reports/a\fb",
		"ESC": "reports/a\x1bb", "513 bytes": long[:pathValueMax+1], "DEL": "reports/a\x7fb",
	} {
		if askable(c) {
			t.Errorf("%s is askable", name)
		}
		if got := candidatesOf("`" + c + "`"); len(got) != 0 {
			t.Errorf("%s: code span offered %q", name, got)
		}
		if got := candidatesOf("see /abs/" + c); len(got) != 0 && slices.ContainsFunc(got, func(s string) bool { return !askable(s) }) {
			t.Errorf("%s: prose offered %q", name, got)
		}
	}
	if !askable(ok) || len(ok) != pathValueMax {
		t.Errorf("a path of exactly %d bytes is refused", pathValueMax)
	}
}

func TestOnlyTheFirstFiftyCandidatesCountAndTheFailedOnesToo(t *testing.T) {
	var b strings.Builder
	for i := range 55 {
		fmt.Fprintf(&b, "`reports/%02d.md` ", i)
	}
	_, cands := linkRefs("f1", b.String(), known("reports/54.md", "reports/49.md"))
	if len(cands) != pathMax || cands[49] != "reports/49.md" {
		t.Fatalf("%d candidates, last %q", len(cands), cands[len(cands)-1])
	}
	md, _ := linkRefs("f1", b.String(), known("reports/54.md", "reports/49.md"))
	if !strings.Contains(md, "(/f1/doc/rel/reports/49.md)") || strings.Contains(md, "reports/54.md](") {
		t.Fatalf("the 50th must link and the 55th stay plain:\n%s", md)
	}
	bad := strings.Repeat("`reports/a\x00b.md` ", 50) + "`reports/ok.md`"
	if _, got := linkRefs("f1", bad, func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("filtered candidates must still count toward the cap: %q", got)
	}
}

func TestAPathInsideAMarkdownLinkOrFenceIsLeftAlone(t *testing.T) {
	src := "[`reports/a.md`](https://example.test) and [/abs/a.md](x)\n\n```\n`reports/b.md`\n```\n\n![/abs/c.md](https://example.test/i.png)"
	md, cands := linkRefs("f1", src, known("reports/a.md", "/abs/a.md", "reports/b.md", "/abs/c.md"))
	if md != src || len(cands) != 0 {
		t.Fatalf("changed:\n%s\n%q", md, cands)
	}
}

func TestALinkedPathKeepsItsTextAndEscapesItsDestination(t *testing.T) {
	rel := func(string) string { return "reports/50%/a#b (1).md" }
	got, _ := linkRefs("f1", "see `reports/x.md` now", rel)
	want := "see [`reports/x.md`](/f1/doc/reports/50%25/a%23b%20%281%29.md) now"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	got, _ = linkRefs("f1", "see /abs/a_b*c.md now", rel)
	if !strings.HasPrefix(got, `see [/abs/a\_b\*c.md](/f1/doc/`) {
		t.Fatalf("got %q", got)
	}
}

func TestAPathAndARefDoNotOverlap(t *testing.T) {
	md, _ := linkRefs("f1", "t1 and /abs/a.md, then a2 `reports/b.md` r3", known("/abs/a.md", "reports/b.md"))
	for _, want := range []string{ref("t1"), "[/abs/a.md](/f1/doc/rel/", ref("a2"), "[`reports/b.md`](/f1/doc/rel/", ref("r3")} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in %q", want, md)
		}
	}
}

func TestPathAtIsCalledOncePerCandidateAndNotAtEveryByte(t *testing.T) {
	calls := 0
	old := pathAt
	pathAt = func(s string, i int) int { calls++; return markdown.PathAt(s, i) }
	defer func() { pathAt = old }()
	src := "x /" + strings.Repeat("a", 5000) + " y"
	linkRefs("f1", src, func(string) string { return "" })
	if calls > len(" x ")+2 {
		t.Fatalf("PathAt called %d times for one candidate in %d bytes", calls, len(src))
	}
	calls = 0
	src = strings.Repeat("/a ", 300)
	linkRefs("f1", src, func(string) string { return "" })
	if calls > 3*pathMax+3 {
		t.Fatalf("PathAt called %d times after the cap of %d candidates", calls, pathMax)
	}
	calls = 0
	src = strings.Repeat("(/.", 3000)
	linkRefs("f1", src, func(string) string { return "" })
	if calls > 5 {
		t.Fatalf("PathAt called %d times on a run no path can come out of", calls)
	}
}

func TestAMemoisedSourceIsNotParsedAgain(t *testing.T) {
	parses := 0
	old := newParser
	newParser = func() *parser.Document { parses++; return old() }
	defer func() { newParser = old }()
	a := &App{Fleet: Fleet{ID: "f1"}}
	a.setPath("f1", "reports/a.md", "reports/a.md", a.now())
	const src = "see `reports/a.md` and t1"
	first := a.linked(src)
	if !strings.Contains(first, "(/f1/doc/reports/a.md)") {
		t.Fatalf("got %q", first)
	}
	for range 5 {
		if a.linked(src) != first {
			t.Fatal("the memo changed its answer")
		}
	}
	if parses != 1 {
		t.Fatalf("parsed %d times", parses)
	}
}

func TestACodeSpanInsideEmphasisAndANodeThatContinuesAWordAreJudgedLikeTheBoard(t *testing.T) {
	for _, src := range []string{
		"x\\_/abs/a.md", "a\\/abs/b.md", "foo*bar*/abs/c.md", "*a*/abs/d.md", "&amp;/abs/f.md", "1\\./abs/g.md",
	} {
		if got, want := candidatesOf(src), boardCandidates(src); !slices.Equal(got, want) {
			t.Errorf("%q: desktop %q board %q", src, got, want)
		}
	}
}
