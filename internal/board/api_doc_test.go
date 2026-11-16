package board_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

const notTextSentence = "This file is not text, so the Board does not show it; download it to open it."

var (
	mp4Bytes = "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"
	mp3Bytes = "ID3\x03\x00\x00\x00\x00\x00\x00\xff\xfb\x90\x00"
	pdfBytes = "%PDF-1.4\n%\xff\xfe\n1 0 obj<<>>endobj\n"
)

func docFiles() map[string]string {
	return map[string]string{
		"reports/t1/design.md": design,
		"reports/t1/api.md":    "# API\n",
		"reports/t1/flow.png":  string(flowPNG),
		"reports/t1/logo.svg":  `<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`,
		"reports/t1/page.html": `<h1>Hi</h1><img src="flow.png" alt="flow">`,
		"reports/t1/data.csv":  "a,b\n1,2\n",
		"reports/t1/data.tsv":  "a\tb\n1\t2\n",
		"reports/t1/notes.txt": "one\ntwo\nthree\n",
		"reports/t1/spec.pdf":  pdfBytes,
		"reports/t1/blob.bin":  "\x00\x01\x02\x03binary",
		"reports/t1/clip.mp4":  mp4Bytes,
		"reports/t1/song.mp3":  mp3Bytes,
		"reports/t1/sub/x.txt": "x\n",
	}
}

func apiDocBoard(t *testing.T, files map[string]string) (http.Handler, string) {
	t.Helper()
	home := fleetFolder(t, files)
	fixed := time.Date(2026, 10, 30, 8, 15, 0, 0, time.UTC)
	if err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			err = os.Chtimes(p, fixed, fixed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st := openNow(t, "f00000000e0aa")
	if _, err := st.RenameFleet(context.Background(), "golden"); err != nil {
		t.Fatal(err)
	}
	return board.New(st, token, board.Options{Home: home, Controls: true, Now: func() time.Time { return frozen }}), home
}

func TestAPIDocGolden(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	for _, c := range []struct{ name, path string }{
		{"doc-folder", "/api/v1/doc/reports/t1"},
		{"doc-file", "/api/v1/doc/reports/t1/design.md"},
		{"doc-image", "/api/v1/doc/reports/t1/flow.png"},
		{"doc-window", "/api/v1/doc/reports/t1/notes.txt?from=2&lines=1"},
	} {
		t.Run(c.name, func(t *testing.T) { checkAPIGolden(t, h, c.name, c.path) })
	}
}

func TestAPIDocFolderIsTheBoardsListing(t *testing.T) {
	h, home := apiDocBoard(t, docFiles())
	_, got := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/t1")
	if got.Path != "reports/t1" || got.Name != "t1" || got.Kind != "folder" || got.At != "2026-10-30T08:15:00Z" || got.Page != 1 || got.PageSize != 500 || got.Total != 13 || got.Pages != 1 {
		t.Fatalf("folder = %+v", got)
	}
	var names, labels []string
	for _, e := range got.Entries {
		suffix := ""
		if e.Dir {
			suffix = "/"
		}
		names = append(names, e.Name+suffix)
		labels = append(labels, e.Label)
		if e.Path != "reports/t1/"+e.Name {
			t.Errorf("entry %s path = %q", e.Name, e.Path)
		}
	}
	page := get(t, h, "/doc/reports/t1")
	var want []string
	for _, m := range regexp.MustCompile(`<span class="name">([^<]*)</span>`).FindAllStringSubmatch(page, -1) {
		want = append(want, html.UnescapeString(m[1]))
	}
	if !slices.Equal(names, want) {
		t.Fatalf("API order %q, page order %q", names, want)
	}
	if got.Entries[0].Name != "sub" || got.Entries[0].Kind != "folder" || got.Entries[0].Label != "DIR" || got.Entries[0].Size != nil {
		t.Errorf("a folder comes first with no size: %+v", got.Entries[0])
	}
	byName := map[string]board.APIDocEntry{}
	for _, e := range got.Entries {
		byName[e.Name] = e
	}
	if e := byName["design.md"]; e.Kind != "file" || e.Label != "MD" || e.Size == nil || *e.Size != int64(len(design)) || e.At != "2026-10-30T08:15:00Z" {
		t.Errorf("design.md = %+v", e)
	}
	if e := byName["flow.png"]; !e.Image || e.Label != "PNG" {
		t.Errorf("flow.png = %+v", e)
	}
	if e := byName["blob.bin"]; e.Image || e.Label != "BIN" {
		t.Errorf("blob.bin = %+v", e)
	}
	if body := request(h, "GET", "/api/v1/doc/reports/t1", nil, true).Body.String(); strings.Contains(body, home) {
		t.Errorf("the listing names the host folder:\n%s", body)
	}
}

func TestAPIDocEmptyFolderHasNoEntriesNotNull(t *testing.T) {
	h, home := apiDocBoard(t, map[string]string{"reports/t1/a.txt": "a"})
	if err := os.Mkdir(filepath.Join(home, "reports", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := request(h, "GET", "/api/v1/doc/reports/empty", nil, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"entries":[]`) || !strings.Contains(rec.Body.String(), `"total":0`) || !strings.Contains(rec.Body.String(), `"pages":1`) {
		t.Fatalf("empty folder = %d:\n%s", rec.Code, rec.Body)
	}
}

func manyFiles(n int, dir string) map[string]string {
	files := map[string]string{}
	for i := range n {
		files[fmt.Sprintf("%s/f%04d.txt", dir, i)] = "x"
	}
	return files
}

func TestAPIDocPagerMatchesTheBoards(t *testing.T) {
	files := manyFiles(1001, "reports/many")
	maps := manyFiles(500, "reports/exact")
	for k, v := range maps {
		files[k] = v
	}
	for k, v := range manyFiles(501, "reports/over") {
		files[k] = v
	}
	h, _ := apiDocBoard(t, files)
	pageNames := func(body string) []string {
		var out []string
		for _, m := range regexp.MustCompile(`<span class="name">([^<]*)</span>`).FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	for _, c := range []struct {
		query              string
		page, pages, count int
		text               string
	}{
		{"", 1, 3, 500, "1,001 entries, page 1 of 3"},
		{"?page=1", 1, 3, 500, "1,001 entries, page 1 of 3"},
		{"?page=2", 2, 3, 500, "1,001 entries, page 2 of 3"},
		{"?page=3", 3, 3, 1, "1,001 entries, page 3 of 3"},
		{"?page=99", 3, 3, 1, "1,001 entries, page 3 of 3"},
	} {
		_, got := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/many"+c.query)
		if got.Page != c.page || got.Pages != c.pages || len(got.Entries) != c.count || got.Total != 1001 || got.PageSize != 500 {
			t.Fatalf("many%s = page %d of %d, %d entries, total %d", c.query, got.Page, got.Pages, len(got.Entries), got.Total)
		}
		body := get(t, h, "/doc/reports/many"+c.query)
		if !strings.Contains(body, c.text) {
			t.Errorf("the page %s does not say %q", c.query, c.text)
		}
		var names []string
		for _, e := range got.Entries {
			names = append(names, e.Name)
		}
		if want := pageNames(body); !slices.Equal(names, want) {
			t.Errorf("many%s: API names differ from the page's (%d vs %d)", c.query, len(names), len(want))
		}
	}
	_, exact := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/exact?page=2")
	if exact.Pages != 1 || exact.Page != 1 || len(exact.Entries) != 500 || exact.Total != 500 {
		t.Errorf("500 entries = %d pages, page %d, %d entries", exact.Pages, exact.Page, len(exact.Entries))
	}
	_, over := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/over?page=2")
	if over.Pages != 2 || over.Page != 2 || len(over.Entries) != 1 || over.Entries[0].Name != "f0500.txt" {
		t.Errorf("501 entries page 2 = %+v", over)
	}
	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?page=1.5", "?page=%20"} {
		e := testAPIError(t, request(h, "GET", "/api/v1/doc/reports/many"+q, nil, true), http.StatusBadRequest, "bad_request")
		if e.Message != "page must be a number, 1 or more" {
			t.Errorf("%s = %q", q, e.Message)
		}
	}
}

func TestAPIDocFileKindsAreWhatTheBoardShows(t *testing.T) {
	big := strings.Repeat("0123456789abcdef\n", (1<<20)/17+10)
	files := docFiles()
	files["reports/t1/big.md"] = big
	h, _ := apiDocBoard(t, files)
	for _, c := range []struct {
		name, kind, query string
		text              bool
		marker            string
	}{
		{"design.md", "markdown", "", true, `class="doc-body`},
		{"big.md", "text", "", true, `class="text-view"`},
		{"flow.png", "image", "?view", false, `class="doc-picture`},
		{"logo.svg", "svg", "", true, `class="doc-picture`},
		{"page.html", "html", "", true, "Preview without scripts"},
		{"spec.pdf", "pdf", "", false, `<iframe class="doc-frame" src="/doc/reports/t1/spec.pdf?raw"`},
		{"data.csv", "csv", "", true, `class="doc-table`},
		{"data.tsv", "tsv", "", true, `class="doc-table`},
		{"notes.txt", "text", "", true, `class="text-view"`},
		{"blob.bin", "binary", "", false, notTextSentence},
		{"clip.mp4", "video", "", false, "<video"},
		{"song.mp3", "audio", "", false, "<audio"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, got := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/t1/"+c.name)
			if got.Kind != c.kind || got.Text != c.text || got.Name != c.name || got.Path != "reports/t1/"+c.name {
				t.Fatalf("%s = %+v", c.name, got)
			}
			if want := int64(len(files["reports/t1/"+c.name])); got.Size != want {
				t.Errorf("size = %d, want %d", got.Size, want)
			}
			contains(t, "the Board's page for "+c.name, get(t, h, "/doc/reports/t1/"+c.name+c.query), c.marker)
		})
	}
	if rec := request(h, "GET", "/doc/reports/t1/flow.png", nil, true); rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("the Board serves a picture's bytes without ?view: %q", rec.Header().Get("Content-Type"))
	}
}

func TestAPIDocNeighboursAreTheBoardsPreviousAndNext(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a.txt", "b.png", "c.md", "d.png", ".hidden", "e.txt", "f.png", "z.txt"} {
		files["reports/n/"+n] = "x\n"
		if strings.HasSuffix(n, ".png") {
			files["reports/n/"+n] = string(flowPNG)
		}
	}
	files["reports/n/sub/q.txt"] = "q"
	h, _ := apiDocBoard(t, files)
	step := regexp.MustCompile(`href="([^"]+)" rel="(prev|next)"`)
	for _, c := range []struct{ name, query, prev, next string }{
		{"a.txt", "", "", "b.png"},
		{"c.md", "", "b.png", "d.png"},
		{"e.txt", "", "d.png", "f.png"},
		{"z.txt", "", "f.png", ""},
		{"b.png", "?view", "", "d.png"},
		{"d.png", "?view", "b.png", "f.png"},
		{"f.png", "?view", "d.png", ""},
	} {
		_, got := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/n/"+c.name)
		var prev, next string
		if got.Prev != nil {
			prev = got.Prev.Name
			if got.Prev.Path != "reports/n/"+prev {
				t.Errorf("%s prev path = %q", c.name, got.Prev.Path)
			}
		}
		if got.Next != nil {
			next = got.Next.Name
			if got.Next.Path != "reports/n/"+next {
				t.Errorf("%s next path = %q", c.name, got.Next.Path)
			}
		}
		if prev != c.prev || next != c.next {
			t.Errorf("%s: prev %q next %q, want %q %q", c.name, prev, next, c.prev, c.next)
		}
		var page, boardNext string
		for _, m := range step.FindAllStringSubmatch(get(t, h, "/doc/reports/n/"+c.name+c.query), -1) {
			name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(m[1], "/doc/reports/n/"), "?view"), "?as=text"), "?view=actual")
			if m[2] == "prev" {
				page = name
			} else {
				boardNext = name
			}
		}
		if page != prev || boardNext != next {
			t.Errorf("%s: API %q %q, Board %q %q", c.name, prev, next, page, boardNext)
		}
	}
	_, only := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/n/sub/q.txt")
	if only.Prev != nil || only.Next != nil {
		t.Errorf("a lone file has neighbours: %+v", only)
	}
}

func TestAPIDocFileKnowsItsFolderPage(t *testing.T) {
	h, _ := apiDocBoard(t, manyFiles(1001, "reports/many"))
	for _, c := range []struct {
		name string
		page int
	}{{"f0000.txt", 1}, {"f0499.txt", 1}, {"f0500.txt", 2}, {"f0999.txt", 2}, {"f1000.txt", 3}} {
		_, got := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/many/"+c.name)
		if got.FolderPage != c.page {
			t.Errorf("%s folder_page = %d, want %d", c.name, got.FolderPage, c.page)
		}
		want := fmt.Sprintf(`<a href="/doc/reports/many?page=%d">many</a>`, c.page)
		if c.page == 1 {
			want = `<a href="/doc/reports/many">many</a>`
		}
		contains(t, c.name, get(t, h, "/doc/reports/many/"+c.name), want)
	}
}

func TestAPIDocRawIsTheBoardsRaw(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	for _, name := range []string{"design.md", "flow.png", "logo.svg", "page.html", "data.csv", "notes.txt", "spec.pdf", "blob.bin", "clip.mp4", "song.mp3"} {
		t.Run(name, func(t *testing.T) {
			want := request(h, "GET", "/doc/reports/t1/"+name+"?raw", nil, true)
			got := request(h, "GET", "/api/v1/doc/reports/t1/"+name+"?raw", nil, true)
			if got.Code != http.StatusOK || want.Code != http.StatusOK || got.Body.String() != docFiles()["reports/t1/"+name] || got.Body.String() != want.Body.String() {
				t.Fatalf("%s: API %d %q, Board %d", name, got.Code, got.Body.String(), want.Code)
			}
			for _, k := range []string{"Content-Type", "Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Content-Disposition", "Cache-Control"} {
				if got.Header().Get(k) != want.Header().Get(k) {
					t.Errorf("%s: %s = %q, Board %q", name, k, got.Header().Get(k), want.Header().Get(k))
				}
			}
			if got.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(got.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Errorf("%s: unsafe headers %v", name, got.Header())
			}
		})
	}
	headers(t, "html", request(h, "GET", "/api/v1/doc/reports/t1/page.html?raw", nil, true), map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Content-Security-Policy": "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'",
	})
	headers(t, "svg", request(h, "GET", "/api/v1/doc/reports/t1/logo.svg?raw", nil, true), map[string]string{"Content-Type": "image/svg+xml", "Content-Security-Policy": "sandbox; default-src 'none'; style-src 'unsafe-inline'"})
	headers(t, "binary", request(h, "GET", "/api/v1/doc/reports/t1/blob.bin?raw", nil, true), map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename=blob.bin`})
}

func TestAPIDocRawNeverInlines(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	inlined := request(h, "GET", "/doc/reports/t1/page.html?frame", nil, true).Body.String()
	if !strings.Contains(inlined, "data:image/png;base64,") {
		t.Fatalf("the Board's frame no longer inlines pictures, so this test pins nothing:\n%s", inlined)
	}
	for _, q := range []string{"?raw", "?raw&frame", "?frame&raw"} {
		rec := request(h, "GET", "/api/v1/doc/reports/t1/page.html"+q, nil, true)
		if rec.Body.String() != docFiles()["reports/t1/page.html"] || strings.Contains(rec.Body.String(), "data:image") {
			t.Errorf("%s inlined the page:\n%s", q, rec.Body)
		}
	}
	rec := request(h, "GET", "/api/v1/doc/reports/t1/page.html?frame", nil, true)
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || strings.Contains(rec.Body.String(), "data:image") {
		t.Errorf("?frame alone must not serve the frame: %q\n%s", rec.Header().Get("Content-Type"), rec.Body)
	}
}

func TestAPIDocRawTakesRangesAndHead(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	req := httptest.NewRequest("GET", "/api/v1/doc/reports/t1/notes.txt?raw", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	req.Header.Set("Range", "bytes=4-6")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "two" || rec.Header().Get("Content-Range") != "bytes 4-6/14" {
		t.Fatalf("range = %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Range"))
	}
	rec = request(h, "HEAD", "/api/v1/doc/reports/t1/notes.txt?raw", nil, true)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "14" {
		t.Fatalf("head = %d, %d bytes, length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
}

func rows(n int, tail string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "row %d <b> & ok\n", i)
	}
	return b.String() + tail
}

func TestAPIDocWindowIsTheBoardsLines(t *testing.T) {
	body := rows(1200, "")
	h, _ := apiDocBoard(t, map[string]string{"reports/t1/lines.txt": body})
	page := get(t, h, "/doc/reports/t1/lines.txt")
	var shown []string
	for _, m := range regexp.MustCompile(`(?s)<pre class="src">(.*?)</pre>`).FindAllStringSubmatch(page, -1) {
		shown = append(shown, strings.Split(strings.TrimSuffix(html.UnescapeString(m[1]), "\n"), "\n")...)
	}
	if len(shown) != 1200 {
		t.Fatalf("the Board shows %d lines", len(shown))
	}
	_, all := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/lines.txt?from=1&lines=2000")
	if all.From != 1 || !slices.Equal(all.Lines, shown) || all.Next != 0 || all.Partial {
		t.Fatalf("whole = from %d, %d lines, next %d, partial %v", all.From, len(all.Lines), all.Next, all.Partial)
	}
	_, mid := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/lines.txt?from=501&lines=100")
	if mid.From != 501 || !slices.Equal(mid.Lines, shown[500:600]) || mid.Next != 601 {
		t.Fatalf("middle = from %d, %d lines, next %d", mid.From, len(mid.Lines), mid.Next)
	}
	_, last := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/lines.txt?from=1200&lines=5")
	if last.From != 1200 || !slices.Equal(last.Lines, shown[1199:]) || last.Next != 0 {
		t.Fatalf("last = %+v", last)
	}
	_, def := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/lines.txt?from=3")
	if len(def.Lines) != 500 || def.Lines[0] != shown[2] || def.Next != 503 {
		t.Fatalf("default count = %d lines, next %d", len(def.Lines), def.Next)
	}
	_, firstOnly := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/lines.txt?lines=2")
	if firstOnly.From != 1 || !slices.Equal(firstOnly.Lines, shown[:2]) || firstOnly.Next != 3 {
		t.Fatalf("lines alone = %+v", firstOnly)
	}
}

func TestAPIDocWindowCapsLinesAndBytes(t *testing.T) {
	h, _ := apiDocBoard(t, map[string]string{
		"reports/t1/three-thousand.txt": rows(3000, ""),
		"reports/t1/wide.txt":           strings.Repeat(strings.Repeat("w", 200<<10)+"\n", 40),
	})
	_, capped := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/three-thousand.txt?lines=100000")
	if len(capped.Lines) != 2000 || capped.Next != 2001 {
		t.Fatalf("lines cap = %d lines, next %d", len(capped.Lines), capped.Next)
	}
	_, wide := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/wide.txt?lines=2000")
	if len(wide.Lines) != 21 || wide.Next != 22 {
		t.Fatalf("byte cap = %d lines, next %d", len(wide.Lines), wide.Next)
	}
}

func TestAPIDocWindowEnds(t *testing.T) {
	h, _ := apiDocBoard(t, map[string]string{
		"reports/t1/open.txt":   "a\nb",
		"reports/t1/closed.txt": "a\nb\n",
		"reports/t1/crlf.txt":   "a\r\nb\r\n",
		"reports/t1/ansi.txt":   "\x1b[31mred\x1b[0m\tx\n",
		"reports/t1/latin.txt":  "ok\ncaf\xe9\n",
		"reports/t1/empty.txt":  "",
		"reports/t1/long.txt":   strings.Repeat("x", 300<<10) + "\nshort\n",
	})
	win := func(name, q string) board.APIDocWindow {
		_, w := apiInto[board.APIDocWindow](t, h, "/api/v1/doc/reports/t1/"+name+q)
		return w
	}
	if w := win("open.txt", "?from=1"); !slices.Equal(w.Lines, []string{"a", "b"}) || !w.Partial || w.Next != 0 {
		t.Errorf("open = %+v", w)
	}
	if w := win("open.txt", "?from=2"); !slices.Equal(w.Lines, []string{"b"}) || !w.Partial {
		t.Errorf("open from 2 = %+v", w)
	}
	if w := win("closed.txt", "?from=1"); !slices.Equal(w.Lines, []string{"a", "b"}) || w.Partial || w.Next != 0 {
		t.Errorf("closed = %+v", w)
	}
	if w := win("closed.txt", "?from=1&lines=1"); !slices.Equal(w.Lines, []string{"a"}) || w.Next != 2 {
		t.Errorf("closed, one line = %+v", w)
	}
	if w := win("crlf.txt", "?from=1"); !slices.Equal(w.Lines, []string{"a", "b"}) {
		t.Errorf("crlf = %q", w.Lines)
	}
	if w := win("ansi.txt", "?from=1"); !slices.Equal(w.Lines, []string{"\x1b[31mred\x1b[0m\tx"}) {
		t.Errorf("ansi = %q", w.Lines)
	}
	if w := win("latin.txt", "?from=1"); !slices.Equal(w.Lines, []string{"ok", "caf\ufffd"}) {
		t.Errorf("latin1 tail = %q", w.Lines)
	}
	if w := win("empty.txt", "?from=1"); w.Lines == nil || len(w.Lines) != 0 || w.Next != 0 {
		t.Errorf("empty = %+v", w)
	}
	long := win("long.txt", "?from=1")
	if len(long.Lines) != 2 || len(long.Lines[0]) != 256<<10 || long.Lines[1] != "short" || len(long.Cut) != 1 || long.Cut[0].Line != 1 || long.Cut[0].Bytes != 300<<10-256<<10 {
		t.Errorf("long = %d lines, first %d bytes, cut %+v", len(long.Lines), len(long.Lines[0]), long.Cut)
	}
	if got := win("long.txt", "?from=2"); got.Cut != nil || got.Lines[0] != "short" {
		t.Errorf("a window past the long line = %+v", got)
	}
	contains(t, "the Board's cut note", get(t, h, "/doc/reports/t1/long.txt"), "… 44 KiB more on this line")
}

func TestAPIDocWindowRefusals(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	for _, c := range []struct{ path, message string }{
		{"reports/t1/notes.txt?from=4", "line 4 is past the end of this file"},
		{"reports/t1/notes.txt?from=999999", "line 999999 is past the end of this file"},
		{"reports/t1/notes.txt?from=0", "from must be a line number, 1 or more"},
		{"reports/t1/notes.txt?from=-3", "from must be a line number, 1 or more"},
		{"reports/t1/notes.txt?from=two", "from must be a line number, 1 or more"},
		{"reports/t1/notes.txt?from=99999999999999999999", "from must be a line number, 1 or more"},
		{"reports/t1/notes.txt?lines=0", "lines must be a number, 1 or more"},
		{"reports/t1/notes.txt?lines=x", "lines must be a number, 1 or more"},
		{"reports/t1/notes.txt?from=1&lines=-1", "lines must be a number, 1 or more"},
		{"reports/t1/blob.bin?from=1", notTextSentence},
		{"reports/t1/blob.bin?lines=5", notTextSentence},
		{"reports/t1/flow.png?from=1", notTextSentence},
		{"reports/t1/spec.pdf?from=1", notTextSentence},
		{"reports/t1?from=1", "a folder has no bytes or lines; ask for its listing"},
		{"reports/t1?lines=1", "a folder has no bytes or lines; ask for its listing"},
		{"reports/t1?raw", "a folder has no bytes or lines; ask for its listing"},
	} {
		e := testAPIError(t, request(h, "GET", "/api/v1/doc/"+c.path, nil, true), http.StatusBadRequest, "bad_request")
		if e.Message != c.message {
			t.Errorf("%s = %q, want %q", c.path, e.Message, c.message)
		}
	}
	contains(t, "the Board's own sentence", get(t, h, "/doc/reports/t1/blob.bin"), notTextSentence)
	if rec := request(h, "GET", "/api/v1/doc/reports/t1/notes.txt?from=1&lines=1&raw", nil, true); rec.Body.String() != "one\ntwo\nthree\n" {
		t.Errorf("raw wins over a window: %q", rec.Body)
	}
}

func TestAPIDocRefusesWhatTheBoardRefuses(t *testing.T) {
	files := docFiles()
	for _, rel := range []string{"hand.db", "HAND.DB", "hand.db-wal", "board.token", "AGENTS.md", "watch.pid", "luvus/server.pid", "locks/t1.lock", "projects/app/README.md", "Projects/app/README.md", "backups/hand.db.1", "cache/models/codex.json", ".git/config", ".env", "reports/.secret/key", "reports/t1/.draft.md"} {
		files[rel] = secret
	}
	h, home := apiDocBoard(t, files)
	sameAsTheBoard(t, h, home, []probe{
		{"..%2F..%2Fhand.db", 403}, {"reports/t1/..%2F..%2Fhand.db", 403}, {"reports%2F..%2Fhand.db", 403}, {"reports/%2E%2E/hand.db", 403},
		{"reports/../hand.db", 307}, {"/etc/passwd", 307}, {"%2Fetc%2Fpasswd", 403}, {"reports%5C..%5Chand.db", 403}, {"reports/t1/design.md%00.png", 403},
		{"reports/t1/./design.md", 307}, {"reports/t1/design.md/.", 307}, {"reports/t1/%2e/design.md", 200},
		{"reports%2Ft1%2Fdesign.md", 200},
		{".git/config", 403}, {"reports/.secret/key", 403}, {"reports/t1/.draft.md", 403}, {"reports/t1/.", 307},
		{"projects", 403}, {"projects/app/README.md", 403}, {"Projects/app/README.md", 403},
		{"board.token", 403}, {"hand.db", 403}, {"HAND.DB", 403}, {"hand.db-wal", 403}, {"watch.pid", 403}, {"AGENTS.md", 403},
		{"luvus/server.pid", 403}, {"locks/t1.lock", 403}, {"backups", 403}, {"cache/models/codex.json", 403},
		{"", 403}, {".", 307}, {"..", 307}, {"reports/t1/missing.md", 404}, {"reports/t1/design.md/x", 404}, {"reports/missing/", 404},
		{"reports/t1/design.md", 200}, {"reports/t1", 200}, {"reports", 200}, {"reports/t1/sub", 200},
	})
}

type probe struct {
	rel  string
	code int
}

func sameAsTheBoard(t *testing.T, h http.Handler, home string, probes []probe) {
	t.Helper()
	codes := map[int]string{403: "forbidden", 404: "not_found", 409: "conflict"}
	real, _ := filepath.EvalSymlinks(home)
	for _, p := range probes {
		for _, q := range []string{"", "?raw", "?from=1", "?page=2"} {
			viaBoard := request(h, "GET", "/doc/"+p.rel+q, nil, true)
			api := request(h, "GET", "/api/v1/doc/"+p.rel+q, nil, true)
			if viaBoard.Code == http.StatusOK && q != "" {
				continue
			}
			want := p.code
			if viaBoard.Code != want && q == "" {
				t.Errorf("/doc/%s = %d, the table says %d", p.rel, viaBoard.Code, want)
			}
			if api.Code != viaBoard.Code && !(viaBoard.Code == http.StatusOK && api.Code == http.StatusBadRequest) {
				t.Errorf("/doc/%s%s: Board %d, API %d:\n%s", p.rel, q, viaBoard.Code, api.Code, api.Body)
				continue
			}
			if strings.Contains(api.Body.String(), secret) || strings.Contains(api.Body.String(), home) || strings.Contains(api.Body.String(), real) {
				t.Errorf("/api/v1/doc/%s%s leaks:\n%s", p.rel, q, api.Body)
			}
			if code, ok := codes[viaBoard.Code]; ok {
				e := testAPIError(t, api, viaBoard.Code, code)
				if e.Message != hint(viaBoard.Result()) {
					t.Errorf("/doc/%s%s: Board says %q, API says %q", p.rel, q, hint(viaBoard.Result()), e.Message)
				}
			}
		}
	}
}

func TestAPIDocNeedsTheSessionAndALoopbackBoard(t *testing.T) {
	h, home := apiDocBoard(t, docFiles())
	for _, q := range []string{"", "?raw", "?from=1"} {
		rec := request(h, "GET", "/api/v1/doc/reports/t1/notes.txt"+q, nil, false)
		testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
		if strings.Contains(rec.Body.String(), "three") {
			t.Errorf("%s served bytes without a cookie", q)
		}
	}
	wrong := httptest.NewRequest("GET", "/api/v1/doc/reports/t1/notes.txt?raw", nil)
	wrong.AddCookie(&http.Cookie{Name: "hand_board", Value: "not-the-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wrong)
	testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
	ro := board.New(open(t), token, board.Options{Home: home, Controls: false})
	for _, q := range []string{"", "?raw", "?from=1"} {
		rec := request(ro, "GET", "/api/v1/doc/reports/t1/notes.txt"+q, nil, true)
		testAPIError(t, rec, http.StatusForbidden, "forbidden")
		if strings.Contains(rec.Body.String(), "three") {
			t.Errorf("a network board served bytes through the API (%s)", q)
		}
	}
	if rec := request(h, "POST", "/api/v1/doc/reports/t1/notes.txt", nil, true); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST = %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestAPIDocAnswersNameNoHostPath(t *testing.T) {
	h, home := apiDocBoard(t, docFiles())
	real, _ := filepath.EvalSymlinks(home)
	for _, p := range []string{"reports", "reports/t1", "reports/t1/design.md", "reports/t1/flow.png", "reports/t1/notes.txt?from=1", "reports/t1/missing.md", "hand.db"} {
		rec := request(h, "GET", "/api/v1/doc/"+p, nil, true)
		for _, bad := range []string{home, real, filepath.ToSlash(home)} {
			if strings.Contains(rec.Body.String(), bad) {
				t.Errorf("%s names the host folder %q:\n%s", p, bad, rec.Body)
			}
		}
		var v map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	_, folder := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/t1")
	for _, e := range folder.Entries {
		if strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, "..") {
			t.Errorf("entry path %q is not fleet-relative", e.Path)
		}
	}
}

func TestAPIDocRouteIsListedAndTakesOnlyReads(t *testing.T) {
	found := false
	for _, rt := range board.APIRoutes() {
		found = found || rt.Method == "GET" && rt.Path == "/doc/{path...}"
	}
	if !found {
		t.Fatal("the API table lacks GET /doc/{path...}")
	}
}

func TestThePhoneDecodesTheDocAnswersWithoutAChange(t *testing.T) {
	envelope := kotlinParamsIn(t, "BoardClient.kt", "class Envelope<T>(")
	var required []string
	for _, p := range envelope {
		if p = strings.TrimSpace(p); p != "" && !strings.Contains(p, " = ") {
			required = append(required, kotlinValName.FindStringSubmatch(p)[1])
		}
	}
	if !slices.Equal(required, []string{"version", "fleet", "data"}) {
		t.Fatalf("the phone's Envelope requires %q", required)
	}
	status := phoneRequired(t, "Status")
	for _, name := range []string{"doc-folder", "doc-file", "doc-image", "doc-window"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "api", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		for _, k := range required {
			if _, ok := root[k]; !ok {
				t.Errorf("%s has no %s, which the phone's Envelope requires", name, k)
			}
		}
		var fleet map[string]string
		if err := json.Unmarshal(root["fleet"], &fleet); err != nil || fleet["id"] == "" || fleet["name"] == "" {
			t.Errorf("%s fleet = %s", name, root["fleet"])
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(root["data"], &data); err != nil {
			t.Fatalf("%s: data is not one object: %v", name, err)
		}
		for _, k := range status {
			if _, ok := data[k]; ok {
				t.Errorf("%s carries %s, a field of the phone's Status", name, k)
			}
		}
	}
}

func TestAPIDocWindowStopsWhenTheClientIsGone(t *testing.T) {
	h, _ := apiDocBoard(t, map[string]string{"reports/t1/flat.txt": strings.Repeat("x", 8<<20), "reports/t1/notes.txt": "one\ntwo\n"})
	for _, p := range []string{"flat.txt?from=1", "notes.txt?from=1", "notes.txt?from=2"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest("GET", "/api/v1/doc/reports/t1/"+p, nil).WithContext(ctx)
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Body.Len() != 0 {
			t.Errorf("%s read and answered for a gone client:\n%.200s", p, rec.Body)
		}
	}
}
