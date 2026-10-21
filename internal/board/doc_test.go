package board_test

import (
	"bytes"
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const secret = "fixture-secret-never-served"

func fleetFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func viewer(t *testing.T, files map[string]string) (http.Handler, string) {
	t.Helper()
	home := fleetFolder(t, files)
	return board.New(open(t), token, board.Options{Home: home, Controls: true}), home
}

func headers(t *testing.T, what string, rec *httptest.ResponseRecorder, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: %s = %q, want %q", what, k, got, v)
		}
	}
}

const design = "# Export design\n\nProposal for t1. See the [API notes](api.md) and the [old notes](../t0/notes.md).\n\n## Goals\n\n- [x] one job per night\n- [ ] a backfill\n\n| stage | runtime |\n|---|---:|\n| extract | 14 min |\n\n```json\n{\"rows\": 1}\n```\n\n![flow](images/flow.png)\n\nRaw HTML stays text: <script>alert(1)</script>\n"

func TestTheViewerRendersMarkdown(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/design.md": design})
	rec := request(h, "GET", "/doc/reports/t1/design.md", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("design = %d:\n%s", rec.Code, rec.Body)
	}
	headers(t, "page", rec, map[string]string{"Content-Type": "text/html; charset=utf-8", "X-Frame-Options": "DENY"})
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("page CSP = %q", csp)
	}
	body := rec.Body.String()
	contains(t, "design", body,
		"<title>design.md · test</title>",
		`<p class="crumbs"><a href="/">Fleets</a> / <a href="/">test</a> / <a href="/doc/reports">reports</a> / <a href="/doc/reports/t1">t1</a></p>`,
		`<h2 class="thread-title">design.md</h2>`,
		`<span class="doc-path">`+filepath.Join(home, "reports", "t1", "design.md")+`</span>`,
		`<a class="doc-download" href="/doc/reports/t1/design.md?download">Download</a>`,
		`<article class="doc-body copy md"><h3>Export design</h3>`,
		`Proposal for <a class="ref" href="/ref/t1">t1</a>`,
		`<a href="api.md">API notes</a>`, `<a href="../t0/notes.md">old notes</a>`,
		`<ul><li class="task done"><span class="sr">done: </span>one job per night</li><li class="task"><span class="sr">to do: </span>a backfill</li></ul>`,
		`<th class="r">runtime</th>`, "<pre><code>{&#34;rows&#34;: 1}</code></pre>",
		`<img src="images/flow.png" alt="flow" loading="lazy">`,
		"&lt;script&gt;alert(1)&lt;/script&gt;")
	lacks(t, "design", body, "<script>alert")
}

func TestTheViewerShowsTextAsPreformatted(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/run.log": "01:00 export <b>ok</b> & done\n"})
	body := get(t, h, "/doc/reports/t1/run.log")
	contains(t, "log", body, `<div class="chunk" data-from="1"><pre class="src">01:00 export &lt;b&gt;ok&lt;/b&gt; &amp; done`+"\n</pre>", `<pre class="gut" aria-hidden="true">1`+"\n</pre>", `<span class="meta">30 B</span>`, `href="/doc/reports/t1/run.log?download"`)
	lacks(t, "log", body, "<b>ok", `class="doc-notice"`)
}

func TestActiveFilesAreShownAsTextAndNeverInline(t *testing.T) {
	files := map[string]string{
		"reports/t1/page.html.txt": "<!doctype html><script>alert(1)</script>",
		"reports/t1/x.svg.txt":     `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"reports/t1/app.js":        "alert(document.cookie)",
		"reports/t1/evil.png":      "<html><script>alert(1)</script>",
	}
	h, _ := viewer(t, files)
	for rel, data := range files {
		rec := request(h, "GET", "/doc/"+rel, nil, true)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(body, `<pre class="src"><span class="partial">`+html.EscapeString(data)+`</span></pre>`) || strings.Contains(body, "<script>alert") || strings.Contains(body, "<svg xmlns") {
			t.Errorf("%s = %d %q:\n%s", rel, rec.Code, rec.Header().Get("Content-Type"), body)
		}
	}
}

var flowPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func TestAnImageIsInlineOnlyWhenBytesAndNameAgree(t *testing.T) {
	h, _ := viewer(t, map[string]string{"shots/a.png": string(flowPNG), "shots/b.JPG": string(flowPNG), "shots/c.md": string(flowPNG), "shots/d.txt": string(flowPNG), "shots/e.png": "not a picture\n"})
	for _, rel := range []string{"shots/a.png", "shots/b.JPG"} {
		rec := request(h, "GET", "/doc/"+rel, nil, true)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), flowPNG) {
			t.Fatalf("%s = %d", rel, rec.Code)
		}
		headers(t, rel, rec, map[string]string{"Content-Type": "image/png", "Content-Disposition": "", "Content-Security-Policy": "sandbox; default-src 'none'", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "private, no-cache"})
	}
	for rel, want := range map[string]string{"shots/c.md": "This file is not text", "shots/d.txt": "This file is not text", "shots/e.png": `<pre class="src">not a picture`} {
		rec := request(h, "GET", "/doc/"+rel, nil, true)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %q:\n%s", rel, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

func TestEveryOtherFileDownloadsAsAnAttachment(t *testing.T) {
	files := map[string]string{
		"reports/t1/export.zip":      "PK\x03\x04" + strings.Repeat("\x00", 64),
		"reports/t1/design.md":       design,
		"reports/t1/flow.png":        string(flowPNG),
		"reports/t1/résumé notes.md": "# notes\n",
	}
	h, _ := viewer(t, files)
	disposition := map[string]string{
		"reports/t1/export.zip":      "attachment; filename=export.zip",
		"reports/t1/design.md":       "attachment; filename=design.md",
		"reports/t1/flow.png":        "attachment; filename=flow.png",
		"reports/t1/résumé notes.md": "attachment; filename*=utf-8''r%C3%A9sum%C3%A9%20notes.md",
	}
	for rel, data := range files {
		rec := request(h, "GET", "/doc/"+(&url.URL{Path: rel}).EscapedPath()+"?download", nil, true)
		if rec.Code != http.StatusOK || rec.Body.String() != data {
			t.Fatalf("%s = %d", rel, rec.Code)
		}
		headers(t, rel, rec, map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": disposition[rel], "Content-Security-Policy": "sandbox; default-src 'none'", "X-Content-Type-Options": "nosniff"})
	}
	page := get(t, h, "/doc/reports/t1/export.zip")
	contains(t, "zip page", page, `<p class="doc-notice">This file is not text, so the Board does not show it; download it to open it.</p>`, `<a class="doc-download" href="/doc/reports/t1/export.zip?download">Download</a>`)
}

func TestTheViewerListsAFolder(t *testing.T) {
	files := map[string]string{"shots/t1/b.png": string(flowPNG), "shots/t1/A.png": string(flowPNG), "shots/t1/notes.txt": "n\n", "shots/t1/sub/x.md": "x", "shots/t1/.draft.md": secret, "shots/t1/.cache/x": secret, "shots/empty/.keep": ""}
	h, _ := viewer(t, files)
	body := get(t, h, "/doc/shots/t1")
	contains(t, "listing", body, `<h2 class="thread-title">t1/</h2>`, `<a href="/doc/shots">shots</a></p>`)
	rows := regexp.MustCompile(`<li><a href="([^"]+)">`).FindAllStringSubmatch(body[strings.Index(body, `<ul class="listing">`):], -1)
	var order []string
	for _, r := range rows {
		order = append(order, r[1])
	}
	if want := []string{"/doc/shots/t1/sub", "/doc/shots/t1/A.png?view", "/doc/shots/t1/b.png?view", "/doc/shots/t1/notes.txt"}; strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("listing order = %v, want %v", order, want)
	}
	contains(t, "listing", body,
		`<li><a href="/doc/shots/t1/sub"><span class="badge" aria-hidden="true">DIR</span><span class="name">sub/</span>`,
		`<li><a href="/doc/shots/t1/A.png?view"><img src="/doc/shots/t1/A.png" alt="" loading="lazy"><span class="name">A.png</span><span class="meta">40 B · <time`,
		`<span class="badge" aria-hidden="true">TXT</span><span class="name">notes.txt</span>`)
	lacks(t, "listing", body, ".draft", ".cache", `class="folder-pager"`, `class="doc-download"`)
	contains(t, "empty", get(t, h, "/doc/shots/empty"), `<li class="quiet">This folder is empty.</li>`)
}

var policy = map[string]string{
	"hidden":  "the Board does not show Hand's own files, dot files, luvus/, locks/ or projects/",
	"outside": "this path leads outside the fleet folder, so the Board does not show it",
	"kind":    "the Board shows files and folders only",
}

func refused(t *testing.T, h http.Handler, path, words string) {
	t.Helper()
	rec := request(h, "GET", path, nil, true)
	if rec.Code != http.StatusForbidden || hint(rec.Result()) != words || strings.Contains(rec.Body.String(), secret) {
		t.Errorf("%s = %d %q, want 403 %q", path, rec.Code, hint(rec.Result()), words)
	}
	if !strings.Contains(rec.Body.String(), `data-tone="fail">403</span>`) {
		t.Errorf("%s is not the error page:\n%s", path, rec.Body)
	}
}

func TestHandsOwnFilesAreRefused(t *testing.T) {
	files := map[string]string{"reports/t1/design.md": design}
	for _, rel := range []string{"hand.db", "hand.db-wal", "board.token", "AGENTS.md", "HAND.DB", "watch.pid", "luvus/server.pid", "locks/t1.lock", "projects/app/README.md", "Projects/app/README.md", "LUVUS/x", "Locks/y", ".git/config", ".env", "reports/.secret/key", "reports/t1/.draft.md"} {
		files[rel] = secret
	}
	h, _ := viewer(t, files)
	for _, path := range []string{"/doc/hand.db", "/doc/hand.db-wal", "/doc/board.token", "/doc/AGENTS.md", "/doc/HAND.DB", "/doc/watch.pid", "/doc/luvus", "/doc/luvus/server.pid", "/doc/locks/t1.lock", "/doc/projects", "/doc/projects/app/README.md", "/doc/Projects/app/README.md", "/doc/LUVUS/x", "/doc/Locks/y", "/doc/.git/config", "/doc/.env", "/doc/reports/.secret/key", "/doc/reports/t1/.draft.md", "/doc/"} {
		refused(t, h, path, policy["hidden"])
	}
	get(t, h, "/doc/reports")
}

func TestTraversalIsRefused(t *testing.T) {
	h, home := viewer(t, map[string]string{"hand.db": secret, "reports/t1/design.md": design})
	for _, c := range []struct {
		path string
		code int
	}{
		{"/doc/..%2F..%2Fhand.db", http.StatusForbidden},
		{"/doc/reports/t1/..%2F..%2Fhand.db", http.StatusForbidden},
		{"/doc/reports%2F..%2Fhand.db", http.StatusForbidden},
		{"/doc/reports/%2E%2E/hand.db", http.StatusForbidden},
		{"/doc/reports/../hand.db", http.StatusTemporaryRedirect},
		{"/doc//etc/passwd", http.StatusTemporaryRedirect},
		{"/doc/reports%5C..%5Chand.db", http.StatusForbidden},
		{"/doc/reports/t1/design.md%00.png", http.StatusForbidden},
		{"/doc/reports/t1/missing.md", http.StatusNotFound},
		{"/doc/reports/t1/design.md/x", http.StatusNotFound},
	} {
		rec := request(h, "GET", c.path, nil, true)
		if rec.Code != c.code || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s = %d, want %d", c.path, rec.Code, c.code)
		}
	}
	if rec := request(h, "GET", "/doc/reports/t1/missing.md", nil, true); hint(rec.Result()) != "there is no file or folder at this path" {
		t.Errorf("missing = %q", hint(rec.Result()))
	}
	for _, rel := range []string{"", ".", "..", "../hand.db", "reports/../../hand.db", "/etc/passwd", filepath.Join(home, "hand.db"), `reports\..\hand.db`, "reports/t1/design.md\x00", "hand.db", "reports/t1/missing.md"} {
		if _, _, _, err := board.Resolve(home, rel); err == nil {
			t.Errorf("Resolve(%q) allowed it", rel)
		}
	}
	if _, _, _, err := board.Resolve("", "templates/doc.html"); err == nil {
		t.Error("Resolve with no fleet home allowed a file in the working directory")
	}
	if rec := request(board.New(open(t), token, board.Options{}), "GET", "/doc/templates/doc.html", nil, true); rec.Code != http.StatusForbidden {
		t.Errorf("a board with no home serves its working directory: %d", rec.Code)
	}
	if _, real, fi, err := board.Resolve(home, "reports/t1/./design.md"); err != nil || filepath.ToSlash(real) != "reports/t1/design.md" || !fi.Mode().IsRegular() {
		t.Errorf("Resolve(design) = %q, %v", real, err)
	}
}

func TestAFileOverOneMiBIsShownNotRefused(t *testing.T) {
	line := "2026-10-19T01:00:00Z export worker heartbeat ok\n"
	big := strings.Repeat(line, (1<<20)/len(line)+1) + "TAIL-MARKER"
	h, _ := viewer(t, map[string]string{"reports/huge.log": big, "reports/huge.md": big})
	for _, rel := range []string{"reports/huge.log", "reports/huge.md"} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body, "heartbeat ok", "TAIL-MARKER", `data-whole="true"`, `href="/doc/`+rel+`?download"`)
		lacks(t, rel, body, "doc-notice", "download it to read it", "up to 1 MiB")
	}
}

func TestABinaryNamedMdIsNotRendered(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/binary.md": "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR# not a heading", "reports/latin1.md": "caf\xe9 # not utf-8"})
	for _, rel := range []string{"reports/binary.md", "reports/latin1.md"} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body, `<p class="doc-notice">This file is not text, so the Board does not show it; download it to open it.</p>`)
		lacks(t, rel, body, "not a heading", "not utf-8", `class="doc-body`)
	}
}

func TestNamesWithSpacesAndUnicodeAreEscaped(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/résumé notes.md": "# Notes\n", "reports/t1/a#b c&d%20.txt": "odd\n", "reports/t 2/x.md": "x"})
	listing := get(t, h, "/doc/reports/t1")
	contains(t, "listing", listing, `href="/doc/reports/t1/r%C3%A9sum%C3%A9%20notes.md"`, `href="/doc/reports/t1/a%23b%20c&amp;d%2520.txt"`, `<span class="name">a#b c&amp;d%20.txt</span>`)
	contains(t, "unicode", get(t, h, "/doc/reports/t1/r%C3%A9sum%C3%A9%20notes.md"), `<h2 class="thread-title">résumé notes.md</h2>`, `href="/doc/reports/t1/r%C3%A9sum%C3%A9%20notes.md?download"`)
	contains(t, "odd", get(t, h, "/doc/reports/t1/a%23b%20c&d%2520.txt"), `<pre class="src">odd`+"\n</pre>")
	contains(t, "space in a folder", get(t, h, "/doc/reports/t%202/x.md"), `<a href="/doc/reports/t%202">t 2</a>`)
}

func TestTheViewerNeedsTheCookie(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/design.md": secret, "reports/t1/flow.png": string(flowPNG)})
	for _, path := range []string{"/doc/reports/t1/design.md", "/doc/reports/t1/design.md?download", "/doc/reports/t1/flow.png", "/doc/reports/t1"} {
		rec := request(h, "GET", path, nil, false)
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), secret) || bytes.Contains(rec.Body.Bytes(), flowPNG) {
			t.Errorf("%s without the cookie = %d", path, rec.Code)
		}
	}
	rec := request(h, "GET", "/doc/reports/t1/design.md?token="+token, nil, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/doc/reports/t1/design.md" {
		t.Fatalf("login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := request(h, "POST", "/doc/reports/t1/design.md", url.Values{"csrf": {token}}, true); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
}

func TestAReadOnlyBoardServesDocuments(t *testing.T) {
	home := fleetFolder(t, map[string]string{"reports/t1/design.md": design, "hand.db": secret})
	h := board.New(open(t), token, board.Options{Home: home, Controls: false})
	contains(t, "network board", get(t, h, "/doc/reports/t1/design.md"), `<article class="doc-body copy md">`, `<p class="crumbs"><a href="/">test</a> / `)
	if rec := request(h, "GET", "/doc/reports/t1/design.md?download", nil, true); rec.Code != http.StatusOK || rec.Body.String() != design {
		t.Fatalf("download = %d", rec.Code)
	}
	refused(t, h, "/doc/hand.db", policy["hidden"])
}

func TestTheViewerIsOneRegion(t *testing.T) {
	h, _ := viewer(t, map[string]string{"reports/t1/design.md": design, "shots/a.txt": "a"})
	for _, path := range []string{"/doc/reports/t1/design.md", "/doc/shots"} {
		body := get(t, h, path)
		if strings.Count(body, "data-region=") != 1 || !strings.Contains(body, `<main class="thread doc" data-region="doc">`) || strings.Contains(body, `id="composer"`) || strings.Contains(body, "http-equiv") {
			t.Fatalf("%s regions:\n%s", path, body)
		}
	}
}

func TestLinksInADocumentReachOnlyTheViewer(t *testing.T) {
	doc := "[api](api.md) [key](../../../.ssh/id_ed25519) [hidden](.ssh/id) [stop](../../../supervisor/stop) [db](../../hand.db)"
	h, _ := viewer(t, map[string]string{"reports/t1/design.md": doc, "reports/t1/api.md": "# API\n", "reports/t1/.ssh/id": secret, "hand.db": secret})
	page := "/doc/reports/t1/design.md"
	want := map[string]int{"api.md": http.StatusOK, "../../../.ssh/id_ed25519": http.StatusNotFound, ".ssh/id": http.StatusForbidden, "../../../supervisor/stop": http.StatusMethodNotAllowed, "../../hand.db": http.StatusForbidden}
	body := get(t, h, page)
	links := regexp.MustCompile(`<a href="([^"]+)">`).FindAllStringSubmatch(body[strings.Index(body, `<article`):], -1)
	if len(links) != len(want) {
		t.Fatalf("links = %v", links)
	}
	for _, l := range links {
		target := (&url.URL{Path: page}).ResolveReference(&url.URL{Path: l[1]}).Path
		rec := request(h, "GET", target, nil, true)
		if rec.Code != want[l[1]] || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s -> %s = %d, want %d", l[1], target, rec.Code, want[l[1]])
		}
	}
}

func TestFileChipsInsideTheFleetOpenTheViewer(t *testing.T) {
	home := fleetFolder(t, map[string]string{"reports/t1/design.md": design, "shots/t2/a.png": string(flowPNG), "hand.db": secret, ".secret/key": secret})
	st := open(t)
	ctx := context.Background()
	for _, c := range []struct{ title, ref, chip string }{
		{"Design", filepath.Join(home, "reports", "t1", "design.md"), `<a class="file" href="/doc/reports/t1/design.md" title="` + filepath.Join(home, "reports", "t1", "design.md") + `" target="_blank" rel="noopener">file: design.md</a>`},
		{"Shots", filepath.Join(home, "shots", "t2") + string(filepath.Separator), `<a class="file" href="/doc/shots/t2" title="` + filepath.Join(home, "shots", "t2") + string(filepath.Separator) + `" target="_blank" rel="noopener">file: t2</a>`},
		{"Missing", filepath.Join(home, "reports", "t3", "gone.md"), `<span class="file" title="` + filepath.Join(home, "reports", "t3", "gone.md") + `">file: gone.md</span>`},
		{"Database", filepath.Join(home, "hand.db"), `<span class="file" title="` + filepath.Join(home, "hand.db") + `">file: hand.db</span>`},
		{"Dot", filepath.Join(home, ".secret", "key"), `<span class="file" title="` + filepath.Join(home, ".secret", "key") + `">file: key</span>`},
		{"Outside", filepath.Join(filepath.Dir(home), "elsewhere.md"), `<span class="file" title="` + filepath.Join(filepath.Dir(home), "elsewhere.md") + `">file: elsewhere.md</span>`},
	} {
		task := active(t, st, c.title)
		a := attempt(t, st, task.ID, state.AttemptRunning, "")
		if _, err := st.AddReport(ctx, a.ID, state.ReportDone, "Done.\n\nDelivered: "+c.ref); err != nil {
			t.Fatal(err)
		}
		ref := state.TaskRef(task.ID)
		h := board.New(st, token, board.Options{Home: home})
		contains(t, c.title+" row", checks(get(t, h, "/"))[ref], c.chip)
		page := get(t, h, "/task/"+ref)
		contains(t, c.title+" task page", page, c.chip)
		if strings.HasPrefix(c.chip, "<a ") {
			href := regexp.MustCompile(`href="([^"]+)"`).FindStringSubmatch(c.chip)[1]
			contains(t, c.title+" task page", page, `<a class="url" href="`+href+`" tabindex="-1" target="_blank" rel="noopener">`)
			get(t, h, href)
		} else {
			lacks(t, c.title+" task page", page, `<a class="url"`)
		}
	}
}

func TestTheDocumentPageIsStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".doc .slug{display:flex;flex-wrap:wrap;align-items:center;gap:4px 12px}",
		".doc-path{min-width:0;color:var(--muted);font-family:var(--mono);font-size:12px;line-height:18px;overflow-wrap:anywhere}",
		".doc-download{display:inline-flex;align-items:center;min-height:var(--control)}",
		".doc-notice{padding:10px 12px;border:1px solid var(--line);border-radius:var(--r-md);background:var(--wait-bg)}",
		".doc-body{min-width:0;max-width:80ch;font-size:15px;line-height:1.6}",
		".doc-body h3{font-size:18px;line-height:24px;margin-top:24px}",
		".doc-body h4{font-size:15px;line-height:22px;margin-top:16px}",
		".md img{display:block;max-width:100%;height:auto;margin:0 0 8px;border:1px solid var(--line);border-radius:var(--r-md)}",
		".md li.task{list-style:none}",
		`.md li.task::before{content:"";display:inline-block;width:12px;height:12px;margin:0 8px 0 -20px;border:1.5px solid var(--line-strong);border-radius:3px;vertical-align:-1px}`,
		".md li.task.done::before{border-color:var(--pass);background:linear-gradient(var(--pass),var(--pass)) center/6px 6px no-repeat}",
		".listing{display:grid;margin:0;padding:0;list-style:none;border-top:1px solid var(--line)}",
		".listing a{display:flex;align-items:center;gap:10px;min-height:44px;padding:0 4px;border-bottom:1px solid var(--line);color:var(--fg)}",
		".listing a:hover{text-decoration:none;background:var(--hover)}",
		".listing img,.listing .badge{flex:none;width:48px}",
		".listing img{height:32px;object-fit:cover;border:1px solid var(--line);border-radius:var(--r-sm);background:var(--card)}",
		".listing .badge{text-align:center}",
		".listing .name{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}",
		".listing .meta{flex:none;white-space:nowrap}")
	contains(t, "touch", block(t, css, "@media (pointer:coarse){textarea,input,select{font-size:16px}"),
		"a.file{padding-block:13px;margin-block:-13px}",
		".delivered a.url{display:flex;align-items:center;min-height:var(--control)}")
}
