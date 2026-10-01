package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

var (
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	inboxRe  = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+\.(png|jpg|webp|gif)$`)
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

func saved(t *testing.T, res *http.Response) (path, name string) {
	t.Helper()
	var out struct{ Path, Name string }
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&out) != nil {
		t.Fatalf("upload = %d %q", res.StatusCode, hint(res))
	}
	return out.Path, out.Name
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
	path, name := saved(t, upload(fx.handler(), "shot.png", pngBytes, token))
	if !inboxRe.MatchString(name) || path != filepath.Join(fx.options.Home, "inbox", name) {
		t.Fatalf("path %q name %q", path, name)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, pngBytes) {
		t.Fatalf("stored %d bytes, %v", len(b), err)
	}
	if fi, err := os.Stat(filepath.Join(fx.options.Home, "inbox")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("inbox mode %v, %v", fi.Mode(), err)
	}
}

func TestOnlyImagesAreAccepted(t *testing.T) {
	fx := newFixture(t)
	for _, data := range []string{"<html><body>x</body></html>", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`} {
		res := upload(fx.handler(), "x.png", []byte(data), token)
		if res.StatusCode != http.StatusUnsupportedMediaType || hint(res) != "only png, jpeg, webp and gif images can be attached" {
			t.Fatalf("%q = %d %q", data, res.StatusCode, hint(res))
		}
	}
	if got := inboxFiles(t, fx.options.Home); len(got) != 0 {
		t.Fatalf("inbox = %q", got)
	}
}

func TestAnImageOverTenMiBIsRefused(t *testing.T) {
	fx := newFixture(t)
	big := append(append([]byte{}, pngBytes...), make([]byte, 10<<20)...)
	if res := upload(fx.handler(), "big.png", big, token); res.StatusCode != http.StatusRequestEntityTooLarge || hint(res) != "an image can be at most 10 MiB" {
		t.Fatalf("10 MiB + 1 = %d %q", res.StatusCode, hint(res))
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
	_, first := saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	_, second := saved(t, upload(fx.handler(), "b.png", pngBytes, token))
	if first != "20261001-120000-1.png" || second != "20261001-120000-2.png" {
		t.Fatalf("names %q %q", first, second)
	}
	if got := inboxFiles(t, fx.options.Home); len(got) != 2 {
		t.Fatalf("inbox = %q", got)
	}
}

func TestUploadsAreAControl(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	if res := upload(fx.handler(), "a.png", pngBytes, token); res.StatusCode != http.StatusForbidden {
		t.Fatalf("network upload = %d", res.StatusCode)
	}
	fx.options.Controls = true
	if res := upload(fx.handler(), "a.png", pngBytes, "wrong"); res.StatusCode != http.StatusForbidden || !strings.Contains(hint(res), "this form is stale") {
		t.Fatalf("bad csrf = %d %q", res.StatusCode, hint(res))
	}
}

func TestInboxImagesAreServed(t *testing.T) {
	fx := newFixture(t)
	_, name := saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	for _, controls := range []bool{true, false} {
		fx.options.Controls = controls
		res := request(fx.handler(), "GET", "/inbox/"+name, nil, true).Result()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cache-Control") != "private, max-age=86400" || !bytes.Equal(body, pngBytes) {
			t.Fatalf("controls %v: %d %v", controls, res.StatusCode, res.Header)
		}
	}
}

func TestOnlyInboxNamesAreServed(t *testing.T) {
	fx := newFixture(t)
	if err := os.WriteFile(filepath.Join(fx.options.Home, "hand.db"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved(t, upload(fx.handler(), "a.png", pngBytes, token))
	for _, path := range []string{"/inbox/..%2Fhand.db", "/inbox/hand.db", "/inbox/20261001-120000-1.html", "/inbox/20261001-120000-1.png%2F..%2F..%2Fhand.db"} {
		rec := request(fx.handler(), "GET", path, nil, true)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%s = %d", path, rec.Code)
		}
	}
}

func TestAnOperatorMessageShowsItsImages(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord(`look\n[image: ` + fx.options.Home + `/inbox/20261001-120000-1.png]`)})
	timeline := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", timeline, `<img src="/inbox/20261001-120000-1.png" alt="Attached image 20261001-120000-1.png" loading="lazy">`, `href="/inbox/20261001-120000-1.png"`, `look`)
	lacks(t, "timeline", timeline, "[image: ")
}

func TestAQueuedMessageShowsItsImages(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.st.AddSupervisorInput(context.Background(), "look\n[image: "+fx.options.Home+"/inbox/20261001-120000-1.png]"); err != nil {
		t.Fatal(err)
	}
	contains(t, "timeline", region(get(t, fx.handler(), "/"), "timeline"), `<img src="/inbox/20261001-120000-1.png" alt="Attached image 20261001-120000-1.png" loading="lazy">`)
}

func TestShotsAreStyledInTheStylesheet(t *testing.T) {
	contains(t, "board.css", asset(t, "board.css"), ".shots img", "max-width:240px", "object-fit:cover")
}

func TestTheComposerAttachesImages(t *testing.T) {
	fx := newFixture(t)
	page := get(t, fx.handler(), "/")
	contains(t, "page", page, `<input type="file" accept="image/png,image/jpeg,image/webp,image/gif" multiple data-attach>`, "Attach images")
	contains(t, "app.js", asset(t, "app.js"), `"paste"`, `"drop"`, `"dragover"`, "data-attach", "/supervisor/image", "[image: ", "Attaching ")
	contains(t, "board.css", asset(t, "board.css"), ".console-box.dropping", ".attach")
}

func TestANetworkBoardHasNoAttachControl(t *testing.T) {
	fx := newFixture(t)
	fx.options.Controls = false
	lacks(t, "page", get(t, fx.handler(), "/"), "data-attach")
}

func TestTheComposerChecksAnImagesSizeBeforeUploading(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "file.size > 10 << 20", "an image can be at most 10 MiB", "busy(files.length)", "reflect()")
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
	contains(t, "board.css", css, "@media (min-width:800px){.console-box:has(.console .start)", `@media (max-width:799px){.console-box{grid-template-columns:minmax(0,1fr) auto;grid-template-areas:"text text" "hint hint" "console send"}}`, ".attach:has(input:focus-visible)", "grid-template-columns:auto minmax(0,1fr) auto")
	lacks(t, "board.css", css, ".attach:focus-within", ".shots:first-child", "minmax(10em,1fr)")
}
