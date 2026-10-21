package board_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func rowsOf(body string) []string {
	var names []string
	for _, r := range regexp.MustCompile(`<span class="name">([^<]+)</span>`).FindAllStringSubmatch(body[strings.Index(body, `<ul class="listing">`):], -1) {
		names = append(names, strings.TrimSuffix(r[1], "/"))
	}
	return names
}

func frames(n int) map[string]string {
	files := map[string]string{"shots/sub/x.md": "x", "shots/sub2/y.md": "y"}
	for i := range n {
		files[fmt.Sprintf("shots/frames/f%04d.txt", i)] = "x"
	}
	return files
}

func TestAFolderPagesAllItsEntries(t *testing.T) {
	files := map[string]string{}
	for i := range 1200 {
		files[fmt.Sprintf("shots/f%04d.txt", i)] = "x"
	}
	files["shots/aaa/x.md"], files["shots/.hidden"] = "x", secret
	h, _ := viewer(t, files)
	one := get(t, h, "/doc/shots")
	rows := rowsOf(one)
	if len(rows) != 500 || rows[0] != "aaa" || rows[1] != "f0000.txt" || rows[499] != "f0498.txt" {
		t.Fatalf("page 1 = %d rows, %v … %v", len(rows), rows[:2], rows[len(rows)-1])
	}
	contains(t, "page 1", one, `<span class="meta">1,201 entries, page 1 of 3</span>`, `<a href="/doc/shots?page=2" rel="next">Next</a>`, `<a href="/doc/shots?page=3">Last</a>`)
	lacks(t, "page 1", one, `rel="prev"`, ">First<", ".hidden", `class="more"`, `class="pager-jump"`)
	if strings.Count(one, `<nav class="folder-pager"`) != 2 {
		t.Error("the pager is not above and below the list")
	}
	two := rowsOf(get(t, h, "/doc/shots?page=2"))
	if len(two) != 500 || two[0] != "f0499.txt" || two[499] != "f0998.txt" {
		t.Fatalf("page 2 = %d rows, %v … %v", len(two), two[0], two[len(two)-1])
	}
	contains(t, "page 2", get(t, h, "/doc/shots?page=2"), "page 2 of 3", `<a href="/doc/shots" rel="prev">Previous</a>`, `<a href="/doc/shots?page=3" rel="next">Next</a>`)
	lacks(t, "page 2", get(t, h, "/doc/shots?page=2"), ">First<", ">Last<")
	last := get(t, h, "/doc/shots?page=3")
	if r := rowsOf(last); len(r) != 201 || r[0] != "f0999.txt" || r[200] != "f1199.txt" {
		t.Fatalf("page 3 = %d rows", len(r))
	}
	contains(t, "last page", last, "page 3 of 3", `<a href="/doc/shots?page=2" rel="prev">Previous</a>`, `<a href="/doc/shots">First</a>`)
	lacks(t, "last page", last, `rel="next"`, ">Last<")
	for _, q := range []string{"?page=99", "?page=3000000000000000000000", "?page=4"} {
		if body := get(t, h, "/doc/shots"+q); !strings.Contains(body, "page 3 of 3") {
			t.Errorf("%s is not the last page", q)
		}
	}
	for _, q := range []string{"?page=0", "?page=-4", "?page=x", "?page="} {
		if body := get(t, h, "/doc/shots"+q); !strings.Contains(body, "page 1 of 3") {
			t.Errorf("%s is not the first page", q)
		}
	}
}

func TestADirectoryAppearsOnceAndFirstWhenPaging(t *testing.T) {
	files := map[string]string{}
	for i := range 700 {
		files[fmt.Sprintf("p/f%03d.txt", i)] = "x"
	}
	for _, d := range []string{"zeta", "alpha"} {
		files["p/"+d+"/x.md"] = "x"
	}
	h, _ := viewer(t, files)
	first, second := rowsOf(get(t, h, "/doc/p")), rowsOf(get(t, h, "/doc/p?page=2"))
	if first[0] != "alpha" || first[1] != "zeta" || first[2] != "f000.txt" || len(first)+len(second) != 702 {
		t.Fatalf("rows = %v %v", first[:3], len(first)+len(second))
	}
}

func TestAPagedFolderOffersAJump(t *testing.T) {
	h, _ := viewer(t, frames(2100))
	body := get(t, h, "/doc/shots/frames?page=3")
	contains(t, "jump", body, "2,100 entries, page 3 of 5", `<a href="/doc/shots/frames">First</a>`, `<a href="/doc/shots/frames?page=5">Last</a>`, `<form class="pager-jump" method="get" action="/doc/shots/frames"><label>Page <input type="number" name="page" min="1" max="5" inputmode="numeric" aria-label="Go to page"></label><button>Go</button></form>`)
	lacks(t, "short folder", get(t, h, "/doc/shots/frames?page=5"), ">Last<")
	h, _ = viewer(t, frames(1200))
	lacks(t, "three pages", get(t, h, "/doc/shots/frames"), "pager-jump")
	contains(t, "three pages", get(t, h, "/doc/shots/frames"), "1,200 entries, page 1 of 3")
}

func TestStepsSkipFoldersAndKeepTheListingOrder(t *testing.T) {
	h, _ := viewer(t, map[string]string{"r/b.md": "# b", "r/a.txt": "a", "r/c.png": string(flowPNG), "r/e.log": "e", "r/sub/z.md": "z", "r/.h.txt": secret, "solo/only.txt": "x"})
	want := map[string][2]string{
		"r/a.txt": {"", "/doc/r/b.md"},
		"r/b.md":  {"/doc/r/a.txt", "/doc/r/c.png?view"},
		"r/c.png": {"/doc/r/b.md", "/doc/r/e.log"},
		"r/e.log": {"/doc/r/c.png?view", ""},
	}
	for rel, w := range want {
		for range 2 {
			body := get(t, h, "/doc/"+rel)
			prev, next := regexp.MustCompile(`<a href="([^"]+)" rel="prev"`).FindStringSubmatch(body), regexp.MustCompile(`<a href="([^"]+)" rel="next"`).FindStringSubmatch(body)
			if rel == "r/c.png" {
				body = get(t, h, "/doc/"+rel+"?view")
				prev, next = regexp.MustCompile(`<a href="([^"]+)" rel="prev"`).FindStringSubmatch(body), regexp.MustCompile(`<a href="([^"]+)" rel="next"`).FindStringSubmatch(body)
				w = [2]string{"", ""}
			}
			got := [2]string{}
			if prev != nil {
				got[0] = strings.ReplaceAll(prev[1], "&amp;", "&")
			}
			if next != nil {
				got[1] = strings.ReplaceAll(next[1], "&amp;", "&")
			}
			if got != w {
				t.Errorf("%s steps %v, want %v", rel, got, w)
			}
		}
	}
	solo := get(t, h, "/doc/solo/only.txt")
	lacks(t, "one file", solo, `class="doc-nav"`, `rel="prev"`, `rel="next"`)
	contains(t, "arrows", get(t, h, "/doc/r/b.md"), `<nav class="doc-nav" aria-label="Files in this folder"><a href="/doc/r/a.txt" rel="prev" aria-keyshortcuts="ArrowLeft">← Previous<span class="meta">a.txt</span></a><a href="/doc/r/c.png?view" rel="next" aria-keyshortcuts="ArrowRight">Next →<span class="meta">c.png</span></a></nav>`)
	lacks(t, "first file", get(t, h, "/doc/r/a.txt"), `rel="prev"`)
	lacks(t, "dot file", get(t, h, "/doc/r/e.log"), ".h.txt")
}

func TestNamesThatDifferOnlyByCaseKeepOneOrder(t *testing.T) {
	h, home := viewer(t, map[string]string{"r/B.txt": "B", "r/c.txt": "c"})
	if err := os.WriteFile(filepath.Join(home, "r", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "r", "B.txt")); string(got) != "B" {
		t.Skip("the file system ignores case")
	}
	for range 3 {
		if got := rowsOf(get(t, h, "/doc/r")); strings.Join(got, " ") != "B.txt b.txt c.txt" {
			t.Fatalf("rows = %v", got)
		}
	}
	contains(t, "B.txt", get(t, h, "/doc/r/B.txt"), `<a href="/doc/r/b.txt" rel="next"`)
	contains(t, "b.txt", get(t, h, "/doc/r/b.txt"), `<a href="/doc/r/B.txt" rel="prev"`, `<a href="/doc/r/c.txt" rel="next"`)
}

func TestTheFolderPageOfAFileIsTheLastCrumb(t *testing.T) {
	h, _ := viewer(t, frames(600))
	contains(t, "first page", get(t, h, "/doc/shots/frames/f0010.txt"), `<a href="/doc/shots/frames">frames</a>`)
	contains(t, "second page", get(t, h, "/doc/shots/frames/f0550.txt"), `<a href="/doc/shots/frames?page=2">frames</a>`)
}

func TestTheImagePageHasFitAndActualSize(t *testing.T) {
	h, _ := viewer(t, map[string]string{"shots/a.png": string(flowPNG), "shots/b.txt": "b", "shots/c.jpg": string(flowPNG), "mix/d.png": "not a picture\n", "shots/e.webp": string(flowPNG)})
	fit := get(t, h, "/doc/shots/c.jpg?view")
	contains(t, "fit", fit, `<h2 class="thread-title">c.jpg</h2>`, `<div class="doc-picture"><img src="/doc/shots/c.jpg" alt="c.jpg"></div>`,
		`<span class="doc-toggle" role="group" aria-label="View"><span class="on" aria-current="true">Fit</span><a href="/doc/shots/c.jpg?view=actual">Actual size</a></span>`,
		`<a class="doc-download" href="/doc/shots/c.jpg?download">Download</a>`,
		`<a href="/doc/shots/a.png?view" rel="prev" aria-keyshortcuts="ArrowLeft">`, `<a href="/doc/shots/e.webp?view" rel="next" aria-keyshortcuts="ArrowRight">`)
	lacks(t, "fit", fit, "data-region=", `class="doc-picture actual"`, "b.txt")
	actual := get(t, h, "/doc/shots/c.jpg?view=actual")
	contains(t, "actual", actual, `<div class="doc-picture actual">`, `<a href="/doc/shots/c.jpg?view">Fit</a><span class="on" aria-current="true">Actual size</span>`,
		`<a href="/doc/shots/a.png?view=actual" rel="prev"`, `<a href="/doc/shots/e.webp?view=actual" rel="next"`)
	rec := request(h, "GET", "/doc/shots/c.jpg", nil, true)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Errorf("the bare route = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	contains(t, "mismatch", get(t, h, "/doc/mix/d.png?view"), `<pre class="src">not a picture`)
	lacks(t, "mismatch", get(t, h, "/doc/mix/d.png?view"), "doc-picture")
	contains(t, "text file", get(t, h, "/doc/shots/b.txt?view"), `id="text-lines"`)
	contains(t, "text file next", get(t, h, "/doc/shots/b.txt"), `<a href="/doc/shots/a.png?view" rel="prev"`, `<a href="/doc/shots/c.jpg?view" rel="next"`)
	headers(t, "view", request(h, "GET", "/doc/shots/c.jpg?view", nil, true), map[string]string{"Content-Type": "text/html; charset=utf-8"})
}

func TestFolderRowsAreEscapedAndPagesNameNoOtherPath(t *testing.T) {
	h, _ := viewer(t, map[string]string{"r/a b#c.png": string(flowPNG), "r/d.txt": "d"})
	contains(t, "folder", get(t, h, "/doc/r"), `<a href="/doc/r/a%20b%23c.png?view"><img src="/doc/r/a%20b%23c.png" alt="" loading="lazy">`)
	contains(t, "page", get(t, h, "/doc/r/d.txt"), `<a href="/doc/r/a%20b%23c.png?view" rel="prev"`)
}

func TestThePagingAndStepsAreStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".listing .refused{display:flex;align-items:center;gap:10px;min-height:44px;padding:0 4px;border-bottom:1px solid var(--line);color:var(--muted)}",
		".listing .refused .meta{max-width:60%;white-space:normal;text-align:right}",
		".folder-pager{display:flex;flex-wrap:wrap;align-items:center;gap:0 12px}",
		".folder-pager a{display:inline-flex;align-items:center;min-height:var(--control)}",
		".pager-jump{display:inline-flex;align-items:center;gap:6px}",
		".pager-jump label{display:inline-flex;align-items:center;gap:6px;color:var(--muted);font-size:12px}",
		".pager-jump input{width:9ch}",
		".doc-nav{display:flex;gap:0 16px}",
		".doc-nav a{display:inline-flex;align-items:center;gap:8px;min-width:0;min-height:var(--control)}",
		".doc-nav a[rel=next]{margin-left:auto}",
		".doc-nav .meta{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}",
		".thread-head>.doc-nav{min-width:0}",
		".doc-picture.actual{overflow:auto;max-height:80vh}",
		".doc-picture.actual img{max-width:none;max-height:none}")
}

func TestTheArrowKeysStepOnlyOutsideInputsSelectionsAndScrollers(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`if (!scope || e.defaultPrevented || e.repeat || e.altKey || e.ctrlKey || e.metaKey) return;`,
		`const step = { ArrowLeft: "prev", ArrowRight: "next" }[e.key];`,
		`const field = e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]");`,
		`e.target.closest("video, audio, .lines, .doc-picture")`,
		`!getSelection()?.isCollapsed`,
		"scope.querySelector(`.doc-nav a[rel=${step}]`)?.click();")
}
