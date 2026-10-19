package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

var (
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	inboxRe  = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+-[A-Za-z0-9_][A-Za-z0-9._-]{0,79}$`)
)

func upload(h http.Handler, name string, data []byte, csrf string) *http.Response {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("csrf", csrf)
	f, _ := w.CreateFormFile("image", name)
	_, _ = f.Write(data)
	_ = w.Close()
	req := httptest.NewRequest("POST", "/supervisor/image", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func hint(res *http.Response) string {
	s, _ := url.PathUnescape(res.Header.Get("X-Hand-Error"))
	return s
}

func saved(t *testing.T, res *http.Response) (path, name, kind string) {
	t.Helper()
	var out struct{ Path, Name, Kind string }
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&out) != nil {
		t.Fatalf("upload = %d %q", res.StatusCode, hint(res))
	}
	return out.Path, out.Name, out.Kind
}

func stamped(t *testing.T, name, want string) {
	t.Helper()
	if !inboxRe.MatchString(name) || name[len("20261001-120000-1-"):] != want {
		t.Fatalf("stored %q, want STAMP-1-%s", name, want)
	}
}

func inboxFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(home, "inbox"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestAnImageIsStoredInTheInbox(t *testing.T) {
	fx := newFixture(t)
	path, name, kind := saved(t, upload(fx.handler(), "shot.png", pngBytes, token))
	stamped(t, name, "shot.png")
	if kind != "image" || path != filepath.Join(fx.options.Home, "inbox", name) {
		t.Fatalf("path %q kind %q", path, kind)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, pngBytes) {
		t.Fatalf("stored %d bytes, %v", len(b), err)
	}
}

var anyFiles = map[string]string{
	"page.html":  "<html><body><script>alert(1)</script></body></html>",
	"logo.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	"app.js":     "alert(1)",
	"report.pdf": "%PDF-1.4\n%%EOF\n",
	"blob.bin":   "\x00\x01\x02\xff",
}

func TestAnyFileIsAccepted(t *testing.T) {
	fx := newFixture(t)
	for name, data := range anyFiles {
		path, stored, kind := saved(t, upload(fx.handler(), name, []byte(data), token))
		stamped(t, stored, name)
		if kind != "file" || path != filepath.Join(fx.options.Home, "inbox", stored) {
			t.Fatalf("%s: path %q kind %q", name, path, kind)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != data {
			t.Fatalf("%s: stored %q, %v", name, b, err)
		}
	}
}

func TestStoredNamesAreSanitised(t *testing.T) {
	fx := newFixture(t)
	for name, want := range map[string]string{
		"../../../etc/cron.d/x":                 "x",
		`..\..\win.ini`:                         "win.ini",
		".bashrc":                               "bashrc",
		"Screenshot 2026-10-19 at 14.37.12.txt": "Screenshot_2026-10-19_at_14.37.12.txt",
		"...":                                   "file",
		"résumé final.pdf":                      "r_sum_final.pdf",
		strings.Repeat("long", 40) + ".pdf":     strings.Repeat("long", 19) + ".pdf",
	} {
		_, stored, _ := saved(t, upload(fx.handler(), name, []byte("text"), token))
		stamped(t, stored, want)
	}
}

func TestANonImageNamedLikeAnImageGetsBin(t *testing.T) {
	fx := newFixture(t)
	for name, want := range map[string]string{
		"evil.png":   "evil.png.bin",
		"photo.JPEG": "photo.JPEG.bin",
		"x.gif":      "x.gif.bin",
		"notes.txt":  "notes.txt",
		strings.Repeat("x", 76) + ".png." + strings.Repeat("a", 20): strings.Repeat("x", 76) + ".bin",
		strings.Repeat("y", 78) + ".webp":                           strings.Repeat("y", 75) + ".bin",
	} {
		_, stored, kind := saved(t, upload(fx.handler(), name, []byte(anyFiles["page.html"]), token))
		stamped(t, stored, want)
		if kind != "file" {
			t.Fatalf("%s: kind %q", name, kind)
		}
	}
}

func TestSniffingBeatsTheExtension(t *testing.T) {
	fx := newFixture(t)
	for name, want := range map[string]string{"shot.txt": "shot.png", "x.gif": "x.png", "pasted": "pasted.png"} {
		_, stored, kind := saved(t, upload(fx.handler(), name, pngBytes, token))
		stamped(t, stored, want)
		if kind != "image" {
			t.Fatalf("%s: kind %q", name, kind)
		}
	}
}

func TestAFileOverTenMiBIsRefused(t *testing.T) {
	fx := newFixture(t)
	big := append(append([]byte{}, pngBytes...), make([]byte, 10<<20)...)
	if res := upload(fx.handler(), "big.png", big, token); res.StatusCode != http.StatusRequestEntityTooLarge || hint(res) != "a file can be at most 10 MiB" {
		t.Fatalf("10 MiB + 1 = %d %q", res.StatusCode, hint(res))
	}
	if res := upload(fx.handler(), "big.zip", make([]byte, 10<<20+1), token); res.StatusCode != http.StatusRequestEntityTooLarge || hint(res) != "a file can be at most 10 MiB" {
		t.Fatalf("zip 10 MiB + 1 = %d %q", res.StatusCode, hint(res))
	}
	huge := append(append([]byte{}, pngBytes...), make([]byte, 30<<20)...)
	if res := upload(fx.handler(), "huge.png", huge, token); res.StatusCode != http.StatusRequestEntityTooLarge || hint(res) != "the request is too large" {
		t.Fatalf("30 MiB = %d %q", res.StatusCode, hint(res))
	}
	if got := inboxFiles(t, fx.options.Home); len(got) != 0 {
		t.Fatalf("inbox = %q", got)
	}
}

func TestTwoImagesInOneSecondKeepBothFiles(t *testing.T) {
	fx := newFixture(t)
	fx.options.Now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local) }
	_, first, _ := saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	_, second, _ := saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	_, third, _ := saved(t, upload(fx.handler(), "b.png", pngBytes, token))
	if first != "20261001-120000-1-a.png" || second != "20261001-120000-2-a.png" || third != "20261001-120000-1-b.png" {
		t.Fatalf("names %q %q %q", first, second, third)
	}
	if got := inboxFiles(t, fx.options.Home); len(got) != 3 {
		t.Fatalf("inbox = %q", got)
	}
}

func TestUploadsAreAControl(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	for name, data := range map[string][]byte{"a.png": pngBytes, "report.pdf": []byte(anyFiles["report.pdf"])} {
		if res := upload(fx.handler(), name, data, token); res.StatusCode != http.StatusForbidden {
			t.Fatalf("network upload of %s = %d", name, res.StatusCode)
		}
	}
	if got := inboxFiles(t, fx.options.Home); len(got) != 0 {
		t.Fatalf("inbox = %q", got)
	}
	fx.options.Controls = true
	if res := upload(fx.handler(), "a.png", pngBytes, "wrong"); res.StatusCode != http.StatusForbidden || !strings.Contains(hint(res), "this form is stale") {
		t.Fatalf("bad csrf = %d %q", res.StatusCode, hint(res))
	}
}

func TestInboxImagesAreServed(t *testing.T) {
	fx := newFixture(t)
	_, name, _ := saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	for _, controls := range []bool{true, false} {
		fx.options.Controls = controls
		res := request(fx.handler(), "GET", "/inbox/"+name, nil, true).Result()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cache-Control") != "private, max-age=86400" || !bytes.Equal(body, pngBytes) {
			t.Fatalf("controls %v: %d %v", controls, res.StatusCode, res.Header)
		}
		if res.Header.Get("Content-Disposition") != "" || res.Header.Get("Content-Security-Policy") == "sandbox; default-src 'none'" {
			t.Fatalf("controls %v: an image is served inline, got %v", controls, res.Header)
		}
	}
}

func TestEveryOtherFileIsServedAsAnAttachment(t *testing.T) {
	fx := newFixture(t)
	for name, data := range anyFiles {
		_, stored, _ := saved(t, upload(fx.handler(), name, []byte(data), token))
		res := request(fx.handler(), "GET", "/inbox/"+stored, nil, true).Result()
		body, _ := io.ReadAll(res.Body)
		h := res.Header
		if res.StatusCode != http.StatusOK || string(body) != data {
			t.Fatalf("%s: %d %q", name, res.StatusCode, body)
		}
		if h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Disposition") != "attachment; filename="+name || h.Get("Content-Security-Policy") != "sandbox; default-src 'none'" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: headers %v", name, h)
		}
	}
	_, stored, _ := saved(t, upload(fx.handler(), "evil.png", []byte(anyFiles["page.html"]), token))
	if h := request(fx.handler(), "GET", "/inbox/"+stored, nil, true).Result().Header; h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Disposition") != "attachment; filename=evil.png.bin" {
		t.Fatalf("html named evil.png: %v", h)
	}
}

func TestAFullDiskIsA507(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	fx.options.Control = func(context.Context, ...string) error {
		return &os.PathError{Op: "write", Path: filepath.Join(fx.options.Home, "inbox", ".upload-1"), Err: syscall.ENOSPC}
	}
	res := post(fx.handler(), "/supervisor/interrupt", nil)
	if res.StatusCode != http.StatusInsufficientStorage || hint(res) != "the disk is full; free some space and try again" {
		t.Fatalf("ENOSPC = %d %q", res.StatusCode, hint(res))
	}
}

func TestOnlyInboxNamesAreServed(t *testing.T) {
	fx := newFixture(t)
	if err := os.WriteFile(filepath.Join(fx.options.Home, "hand.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	for _, path := range []string{"/inbox/..%2Fhand.db", "/inbox/hand.db", "/inbox/20261001-120000-1.html", "/inbox/20261001-120000-1.png%2F..%2F..%2Fhand.db", "/inbox/20261001-120000-1-..%2F..%2Fhand.db", "/inbox/20261001-120000-1-.env", "/inbox/20261001-120000-1-a%2F..%2F..%2Fhand.db"} {
		rec := request(fx.handler(), "GET", path, nil, true)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%s = %d", path, rec.Code)
		}
	}
}

func inboxFile(t *testing.T, home, name string, size int) string {
	t.Helper()
	path := filepath.Join(home, "inbox", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnOperatorMessageShowsItsImages(t *testing.T) {
	fx := newFixture(t)
	inboxFile(t, fx.options.Home, "20261001-120000-1.png", 40)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord(`look\n[image: ` + strings.ReplaceAll(fx.options.Home, `\`, `\\`) + `/inbox/20261001-120000-1.png]`)})
	timeline := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", timeline, `<img src="/inbox/20261001-120000-1.png" alt="Attached image 20261001-120000-1.png" loading="lazy">`, `href="/inbox/20261001-120000-1.png"`, `look`)
	lacks(t, "timeline", timeline, "[image: ")
}

func TestAQueuedMessageShowsItsImages(t *testing.T) {
	fx := newFixture(t)
	inboxFile(t, fx.options.Home, "20261001-120000-1.png", 40)
	if _, err := fx.st.AddSupervisorInput(context.Background(), "look\n[image: "+fx.options.Home+"/inbox/20261001-120000-1.png]"); err != nil {
		t.Fatal(err)
	}
	contains(t, "timeline", region(get(t, fx.handler(), "/"), "timeline"), `<img src="/inbox/20261001-120000-1.png" alt="Attached image 20261001-120000-1.png" loading="lazy">`)
}

func TestAMessageShowsAFileCardForEachFile(t *testing.T) {
	fx := newFixture(t)
	pdf := inboxFile(t, fx.options.Home, "20261001-120000-1-report.pdf", 312320)
	shot := inboxFile(t, fx.options.Home, "20261001-120000-2-shot.png", 40)
	gone := filepath.Join(fx.options.Home, "inbox", "20261001-120000-3-notes.txt")
	lost := filepath.Join(fx.options.Home, "inbox", "20261001-120000-4.png")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{"see these", "[file: " + pdf + "]", "[image: " + shot + "]", "[file: " + gone + "]", "[image: " + lost + "]", "[file: " + outside + "]", "[file: " + fx.options.Home + "/inbox/../hand.db]"}, "\n")
	if _, err := fx.st.AddSupervisorInput(context.Background(), text); err != nil {
		t.Fatal(err)
	}
	timeline := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", timeline,
		`<a class="sent-file" href="/inbox/20261001-120000-1-report.pdf" download="report.pdf" title="`+template.HTMLEscapeString(pdf)+`"><span class="badge" aria-hidden="true">PDF</span><span class="sent-name">report.pdf</span><span class="meta">PDF · 305 KiB</span></a>`,
		`<img src="/inbox/20261001-120000-2-shot.png" alt="Attached image shot.png" loading="lazy">`,
		`<span class="sent-file missing" title="`+template.HTMLEscapeString(gone)+`"><span class="badge" aria-hidden="true">TXT</span><span class="sent-name">notes.txt</span><span class="meta">no longer in inbox/</span></span>`,
		`<span class="badge" aria-hidden="true">PNG</span><span class="sent-name">20261001-120000-4.png</span><span class="meta">no longer in inbox/</span>`,
		template.HTMLEscapeString("[file: "+outside+"]"),
		template.HTMLEscapeString("[file: "+fx.options.Home+"/inbox/../hand.db]"),
	)
	lacks(t, "timeline", timeline, "[file: "+template.HTMLEscapeString(pdf), `src="/inbox/20261001-120000-4.png"`, `href="/inbox/20261001-120000-3-notes.txt"`)
}

func TestShotsAreStyledInTheStylesheet(t *testing.T) {
	contains(t, "board.css", asset(t, "board.css"), ".shots img", "max-width:240px", "object-fit:cover")
}

func TestFileCardsAreStyledInTheStylesheet(t *testing.T) {
	contains(t, "board.css", asset(t, "board.css"),
		".shots .sent-file{display:grid;grid-template-columns:auto minmax(0,1fr);align-items:center;align-self:start;column-gap:10px;min-width:0;max-width:min(100%,280px);padding:2px 12px 2px 8px;border:1px solid var(--line);border-radius:var(--r-md);background:var(--bg);color:var(--fg);line-height:20px}",
		".sent-file>.badge{grid-row:1/3}",
		".sent-file>:not(.badge){min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}",
		".shots a.sent-file:hover{border-color:var(--line-strong);text-decoration:none}",
		".shots .sent-file.missing{border-style:dashed;color:var(--muted)}",
	)
}

func TestTheComposerAttachesAnyFile(t *testing.T) {
	fx := newFixture(t)
	page := get(t, fx.handler(), "/")
	contains(t, "page", page, `<input type="file" multiple data-attach>`, `title="Attach files"`, `<span class="sr">Attach files</span>`)
	lacks(t, "page", page, "accept=", "Attach images")
	js := asset(t, "app.js")
	contains(t, "app.js", js, `"paste"`, `"drop"`, `"dragover"`, "data-attach", "/supervisor/image", "const { path, kind } = await res.json();", "put(`[${kind}: ${path}]`);", `items.filter((i) => i.kind === "file").map`, "Attaching ${running} file")
	lacks(t, "app.js", js, `i.type.startsWith("image/")`, "put(`[image: ${path}]`)")
	contains(t, "board.css", asset(t, "board.css"), ".console-box.dropping", ".attach")
}

func TestANetworkBoardHasNoAttachControl(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	lacks(t, "page", get(t, fx.handler(), "/"), "data-attach")
}

func TestTheComposerChecksAFilesSizeBeforeUploading(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "file.size > 10 << 20", "a file can be at most 10 MiB", "busy(files.length)", "reflect()")
}

func TestABigURLEncodedPostIsTooLarge(t *testing.T) {
	fx := newFixture(t)
	res := post(fx.handler(), "/supervisor/send", url.Values{"text": {strings.Repeat("x", 12<<20)}})
	if res.StatusCode != http.StatusRequestEntityTooLarge || hint(res) != "the request is too large" {
		t.Fatalf("12 MiB form = %d %q", res.StatusCode, hint(res))
	}
}

func TestASymlinkInTheInboxIsNotFollowed(t *testing.T) {
	fx := newFixture(t)
	if err := os.WriteFile(filepath.Join(fx.options.Home, "hand.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fx.options.Home, "inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../hand.db", filepath.Join(fx.options.Home, "inbox", "20261001-120000-9.png")); err != nil {
		t.Fatal(err)
	}
	rec := request(fx.handler(), "GET", "/inbox/20261001-120000-9.png", nil, true)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("symlink = %d", rec.Code)
	}
}

func TestTheComposerLayoutKeepsTheHintReadable(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css, ".composer-foot .hint{flex:1;min-width:0;line-height:18px}", "@container (max-width:519px){[data-send-key]{display:none}}", ".attach:has(input:focus-visible)")
	lacks(t, "board.css", css, ".attach:focus-within", ".shots:first-child", "minmax(10em,1fr)")
}

func TestTheComposerWaitsForItsUploads(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, `"attaching" in form.dataset`, "Wait for the files to finish attaching", "const at = text.selectionEnd ?? text.value.length")
	lacks(t, "app.js", js, "text.selectionStart ?? text.value.length")
}

func TestEveryAttachEndsItsUploadOnce(t *testing.T) {
	js := asset(t, "app.js")
	if strings.Count(js, "busy(-1)") != 1 || !strings.Contains(js, "} finally {") || !strings.Contains(js, "The file could not be attached") {
		t.Fatal("attach must release each upload once, in a finally, after the line is inserted")
	}
}
