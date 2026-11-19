package desktop

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/markdown"
)

func ref(r string) string { return "[" + r + "](/f1/ref/" + r + ` "Open ` + r + `")` }

var refCases = []struct {
	name, in string
	want     []string
}{
	{"four kinds", "t1 a2 d3 r4", []string{"t1", "a2", "d3", "r4"}},
	{"worker and issue stay plain", "w5 #6 owner/repo#7", nil},
	{"leading zero", "t01 t0 a007", nil},
	{"after word char", "at1 xt1 1t1 pt1", nil},
	{"after underscore", "x_t1 _t1", nil},
	{"after slash", "/t1 a/t1 foo/d1", nil},
	{"after backslash", `\t1`, nil},
	{"before word char", "t1x t1_ t12abc", nil},
	{"before dot word", "t1.go t1.x", nil},
	{"before dot space or end", "see t1. then t2.", []string{"t1", "t2"}},
	{"adjacent punctuation", "(t1), [a2]; \"d3\": r4!", []string{"t1", "a2", "d3", "r4"}},
	{"comma and dash", "t1,t2 a1-a2", []string{"t1", "t2", "a1", "a2"}},
	{"unicode before", "éa1 日t1", nil},
	{"unicode after", "a1é t1日", nil},
	{"unicode space", "x\u00a0t1", nil},
	{"long digit run", "t" + strings.Repeat("9", 400), []string{"t" + strings.Repeat("9", 400)}},
	{"no digits", "t a d r tdar", nil},
	{"code span", "run `t1` and ``a2 `x` ``", nil},
	{"code span then plain", "`t1` t2", []string{"t2"}},
	{"fence", "```\nt1\n```\n\nt2", []string{"t2"}},
	{"tilde fence", "~~~\nd1\n~~~\n\nd2", []string{"d2"}},
	{"indented code", "para\n\n    t1\n\nt2", []string{"t2"}},
	{"link text", "[t1](https://example.com/t1) t2", []string{"t2"}},
	{"link destination", "[x](/f1/ref/t1) and [y](https://example.com/a2)", nil},
	{"link to a bare name", "[y](t3)", nil},
	{"reference link", "[t1][x] and [x]\n\n[x]: https://example.com", nil},
	{"image alt", "![t1](https://example.com/a.png)", nil},
	{"autolink", "<https://example.com/t1> t2", []string{"t2"}},
	{"raw url", "https://example.com/a/t1 and https://example.com/?x=a2 t3", []string{"t3"}},
	{"www url", "www.example.com/t1 t2", []string{"t2"}},
	{"email", "me@t1.example t2", []string{"t2"}},
	{"inline html", "<span title=\"t1\">t2</span>", []string{"t2"}},
	{"html block", "<div>\nt1\n</div>\n\nt2", []string{"t2"}},
	{"heading", "# Plan for t1\n\n## a2", []string{"t1", "a2"}},
	{"list item", "- t1\n- a2\n  - d3\n1. r4", []string{"t1", "a2", "d3", "r4"}},
	{"table cell", "| ref | note |\n|---|---|\n| t1 | a2 |", []string{"t1", "a2"}},
	{"blockquote", "> see t1\n> and a2", []string{"t1", "a2"}},
	{"emphasis", "*t1* **a2** ~~d3~~ _r4_", []string{"t1", "a2", "d3", "r4"}},
	{"emphasis edge", "_see t1_ **d3.**", []string{"t1", "d3"}},
	{"double underscore", "__a2__", []string{"a2"}},
	{"underscore inside word", "snake_t1_case", nil},
	{"escaped", `\*t1\*`, []string{"t1"}},
	{"entity", "a &amp; t1", []string{"t1"}},
	{"line start and end", "t1\na2\n\nd3\n", []string{"t1", "a2", "d3"}},
	{"crlf", "t1\r\na2\r\n", []string{"t1", "a2"}},
	{"already linked", "[t1](/f1/ref/t1) t2", []string{"t2"}},
	{"footnote", "x[^1]\n\n[^1]: see t1", []string{"t1"}},
	{"empty", "", nil},
}

var boardDiffers = map[string]string{
	"tilde fence":         "d1 d2",
	"indented code":       "t1 t2",
	"link to a bare name": "t3",
	"reference link":      "t1",
	"inline html":         "t1 t2",
	"html block":          "t1 t2",
	"double underscore":   "",
	"already linked":      "t1 t2",
}

func TestLinkRefsGrammar(t *testing.T) {
	for _, tc := range refCases {
		t.Run(tc.name, func(t *testing.T) {
			got := linkRefs("f1", tc.in)
			var found []string
			for _, m := range regexp.MustCompile(`\[([tdar][0-9]+)\]\(/f1/ref/([tdar][0-9]+) "Open ([tdar][0-9]+)"\)`).FindAllStringSubmatch(got, -1) {
				if m[1] != m[2] || m[2] != m[3] {
					t.Fatalf("link %q is not for one ref", m[0])
				}
				found = append(found, m[1])
			}
			if strings.Join(found, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("linkRefs(%q)\n got %q\nwant refs %q", tc.in, got, tc.want)
			}
			if len(tc.want) == 0 && got != tc.in {
				t.Fatalf("linkRefs(%q) changed the text: %q", tc.in, got)
			}
		})
	}
}

func TestLinkRefsAgreesWithTheBoard(t *testing.T) {
	board := regexp.MustCompile(`<a class="ref" href="/f1/ref/[^"]*">([tdar][0-9]+)</a>`)
	for _, tc := range refCases {
		t.Run(tc.name, func(t *testing.T) {
			var want []string
			for _, m := range board.FindAllStringSubmatch(string(markdown.RenderRefs(tc.in, "/f1", nil, nil)), -1) {
				want = append(want, m[1])
			}
			expect := strings.Join(tc.want, " ")
			if d, ok := boardDiffers[tc.name]; ok {
				expect = d
			}
			if strings.Join(want, " ") != expect {
				t.Fatalf("Board links %q in %q, expected %q", want, tc.in, expect)
			}
		})
	}
}

func TestLinkRefsOtherFleetIDs(t *testing.T) {
	for _, id := range []string{"", "a b", "a)b", "a/b", "é", "a\"b", "a\nb"} {
		if got := linkRefs(id, "t1"); got != "t1" {
			t.Errorf("fleet %q: %q", id, got)
		}
	}
	if got := linkRefs("fab28ae721878", "t1"); got != "[t1](/fab28ae721878/ref/t1 \"Open t1\")" {
		t.Errorf("got %q", got)
	}
}

func TestLinkRefsIsIdempotentOnItsOutput(t *testing.T) {
	once := linkRefs("f1", "t1 and `a2` and [d3](x) r4")
	if twice := linkRefs("f1", once); twice != once {
		t.Fatalf("second pass changed %q to %q", once, twice)
	}
}

func TestLinkRefsStaysLinearInTheInlinesOfOneParagraph(t *testing.T) {
	src := strings.Repeat("*a* t1 ", 20000)
	start := time.Now()
	got := linkRefs("f1", src)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("linkRefs took %v", d)
	}
	if n := strings.Count(got, "/f1/ref/t1"); n != 20000 {
		t.Fatalf("linked %d refs, want 20000", n)
	}
}

func TestTheRefMemoKeepsWhatItLinkedAndStaysBounded(t *testing.T) {
	a := &App{Fleet: Fleet{ID: "f1"}}
	if got := a.linked("see t1"); got != "see "+ref("t1") {
		t.Fatalf("got %q", got)
	}
	k := refKey{"f1", "see t1"}
	a.refMemo[k] = "kept"
	if got := a.linked("see t1"); got != "kept" {
		t.Fatalf("the memo was not read: %q", got)
	}
	for i := 0; i < 3*refMemoMax; i++ {
		a.linked("t" + strconv.Itoa(i+1))
		if len(a.refMemo) > refMemoMax {
			t.Fatalf("memo holds %d entries after %d", len(a.refMemo), i+1)
		}
	}
	big := strings.Repeat("x ", refMemoSrcMax) + "t1"
	a.linked(big)
	if _, ok := a.refMemo[refKey{"f1", big}]; ok {
		t.Fatal("a source over the cap was kept")
	}
	mid := strings.Repeat("x ", refMemoSrcMax/4)
	for i := 0; i < 200; i++ {
		a.linked(mid + strconv.Itoa(i) + " t1")
		if a.refMemoBytes > refMemoBytesMax {
			t.Fatalf("memo holds %d bytes", a.refMemoBytes)
		}
	}
}
