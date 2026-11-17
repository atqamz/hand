package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

func apiUpload(h http.Handler, field, name string, data []byte, csrf string, cookie bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, _ := w.CreateFormFile(field, name)
	_, _ = f.Write(data)
	_ = w.Close()
	req := httptest.NewRequest("POST", "/api/v1/inbox", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func apiStored(t *testing.T, h http.Handler, name string, data []byte) board.APIInboxFile {
	t.Helper()
	_, got := apiBody[board.APIInboxFile](t, apiUpload(h, "file", name, data, token, true))
	return got
}

func apiInboxFixture(t *testing.T) *fixture {
	t.Helper()
	return apiFixture(t, func() time.Time { return frozen })
}

func TestAPIInboxGolden(t *testing.T) {
	fx := apiInboxFixture(t)
	rec := apiUpload(fx.handler(), "file", "shot.png", pngBytes, token, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /inbox = %d:\n%s", rec.Code, rec.Body)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	out.WriteByte('\n')
	file := filepath.Join("testdata", "api", "inbox-stored.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(file, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to write it)", err)
	}
	if out.String() != string(want) {
		t.Fatalf("POST /inbox does not match %s:\n%s", file, out.String())
	}
}

func TestAPIInboxStoresWhatTheBoardStores(t *testing.T) {
	long := strings.Repeat("long", 40) + ".pdf"
	cases := []struct {
		name string
		data []byte
	}{
		{"shot.png", pngBytes}, {"shot.txt", pngBytes}, {"pasted", pngBytes}, {"x.gif", pngBytes},
		{"../../../etc/cron.d/x", []byte("text")}, {`..\..\win.ini`, []byte("text")}, {".bashrc", []byte("text")},
		{"Screenshot 2026-10-19 at 14.37.12.txt", []byte("text")}, {"...", []byte("text")}, {"résumé final.pdf", []byte("text")}, {long, []byte("text")},
		{"evil.png", []byte(anyFiles["page.html"])}, {"photo.JPEG", []byte("text")}, {strings.Repeat("x", 76) + ".png." + strings.Repeat("a", 20), []byte("text")},
		{"a.png", pngBytes}, {"a.png", pngBytes}, {"b.png", pngBytes},
		{"report.pdf", []byte(anyFiles["report.pdf"])}, {"blob.bin", []byte(anyFiles["blob.bin"])},
	}
	api, page := apiInboxFixture(t), newFixture(t)
	page.options.Now = api.options.Now
	for _, c := range cases {
		got := apiStored(t, api.handler(), c.name, c.data)
		path, name, kind := saved(t, upload(page.handler(), c.name, c.data, token))
		if got.Name != name || got.Kind != kind || got.Label != name[len("20261101-050000-1-"):] || got.Size != int64(len(c.data)) {
			t.Errorf("%q: API %+v, Board %q %q", c.name, got, name, kind)
		}
		a, _ := os.ReadFile(filepath.Join(api.options.Home, "inbox", got.Name))
		b, _ := os.ReadFile(path)
		if !bytes.Equal(a, c.data) || !bytes.Equal(b, c.data) {
			t.Errorf("%q: stored bytes differ", c.name)
		}
	}
	if a, b := inboxFiles(t, api.options.Home), inboxFiles(t, page.options.Home); !slices.Equal(a, b) {
		t.Fatalf("API inbox %q, Board inbox %q", a, b)
	}
}

func TestAPIInboxAnswerNamesNoHostPath(t *testing.T) {
	fx := apiInboxFixture(t)
	rec := apiUpload(fx.handler(), "file", "shot.png", pngBytes, token, true)
	real, _ := filepath.EvalSymlinks(fx.options.Home)
	for _, bad := range []string{fx.options.Home, real, filepath.ToSlash(fx.options.Home), "inbox/", `"path"`} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("the answer holds %q:\n%s", bad, rec.Body)
		}
	}
	if rec.Header().Get("X-Hand-Receipt") != "" {
		t.Errorf("receipt = %q", rec.Header().Get("X-Hand-Receipt"))
	}
}

func TestAPIInboxCapsAreTheBoards(t *testing.T) {
	fx := apiInboxFixture(t)
	h := fx.handler()
	exact := append(append([]byte{}, pngBytes...), make([]byte, 10<<20-len(pngBytes))...)
	if got := apiStored(t, h, "exact.png", exact); got.Size != 10<<20 {
		t.Fatalf("exactly 10 MiB = %+v", got)
	}
	before := len(inboxFiles(t, fx.options.Home))
	for _, c := range []struct {
		what, name string
		data       []byte
		msg        string
	}{
		{"an image over 10 MiB", "big.png", append(append([]byte{}, pngBytes...), make([]byte, 10<<20)...), "a file can be at most 10 MiB"},
		{"a file over 10 MiB", "big.zip", make([]byte, 10<<20+1), "a file can be at most 10 MiB"},
		{"a post over 11 MiB", "huge.png", append(append([]byte{}, pngBytes...), make([]byte, 30<<20)...), "the request is too large"},
	} {
		e := testAPIError(t, apiUpload(h, "file", c.name, c.data, token, true), http.StatusRequestEntityTooLarge, "too_large")
		if e.Message != c.msg {
			t.Errorf("%s: %q, want %q", c.what, e.Message, c.msg)
		}
	}
	if got := len(inboxFiles(t, fx.options.Home)); got != before {
		t.Fatalf("a refused upload left %d files in the inbox", got-before)
	}
}

func TestAPIInboxUploadRefusals(t *testing.T) {
	fx := apiInboxFixture(t)
	h := fx.handler()
	testAPIError(t, apiUpload(h, "file", "a.png", pngBytes, "", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiUpload(h, "file", "a.png", pngBytes, "wrong", true), http.StatusForbidden, "csrf_required")
	testAPIError(t, apiUpload(h, "file", "a.png", pngBytes, token, false), http.StatusUnauthorized, "unauthorized")
	testAPIError(t, apiUpload(h, "image", "a.png", pngBytes, token, true), http.StatusBadRequest, "bad_request")
	json := httptest.NewRequest("POST", "/api/v1/inbox", strings.NewReader(`{"file":"x"}`))
	json.Header.Set("Content-Type", "application/json")
	json.Header.Set("X-Hand-CSRF", token)
	json.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, json)
	testAPIError(t, rec, http.StatusBadRequest, "bad_request")
	fx.options.Controls = false
	testAPIError(t, apiUpload(fx.handler(), "file", "a.png", pngBytes, token, true), http.StatusForbidden, "forbidden")
	fx.options.Controls = true
	fx.options.Control = nil
	testAPIError(t, apiUpload(fx.handler(), "file", "a.png", pngBytes, token, true), http.StatusForbidden, "forbidden")
	if got := inboxFiles(t, fx.options.Home); len(got) != 0 {
		t.Fatalf("a refused upload left %q", got)
	}
}

func TestAPIInboxStoreFailureKeepsTheHostPathOut(t *testing.T) {
	fx := apiInboxFixture(t)
	if err := os.WriteFile(filepath.Join(fx.options.Home, "inbox"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := testAPIError(t, apiUpload(fx.handler(), "file", "a.png", pngBytes, token, true), http.StatusInternalServerError, "internal")
	if strings.Contains(e.Message, fx.options.Home) {
		t.Fatalf("the refusal names the host folder: %q", e.Message)
	}
}

func TestAPIInboxRoutes(t *testing.T) {
	var post, get bool
	for _, rt := range board.APIRoutes() {
		post = post || rt.Method == "POST" && rt.Path == "/inbox"
		get = get || rt.Method == "GET" && rt.Path == "/inbox/{name}"
	}
	if !post || !get {
		t.Fatalf("the API table lacks POST /inbox (%v) or GET /inbox/{name} (%v)", post, get)
	}
	fx := apiInboxFixture(t)
	h := fx.handler()
	got := apiStored(t, h, "a.png", pngBytes)
	if rec := request(h, "POST", "/api/v1/inbox/"+got.Name, nil, true); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST /inbox/name = %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := request(h, "GET", "/api/v1/inbox", nil, true); rec.Code != http.StatusNotFound {
		t.Errorf("GET /inbox = %d", rec.Code)
	}
	if rec := request(h, "PUT", "/api/v1/inbox", nil, true); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("PUT /inbox = %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestAPIInboxServesWhatTheBoardServes(t *testing.T) {
	fx := apiInboxFixture(t)
	h := fx.handler()
	files := map[string][]byte{"shot.png": pngBytes, "evil.png": []byte(anyFiles["page.html"])}
	for name, data := range anyFiles {
		files[name] = []byte(data)
	}
	for orig, data := range files {
		got := apiStored(t, h, orig, data)
		for _, method := range []string{"GET", "HEAD"} {
			page := request(h, method, "/inbox/"+got.Name, nil, true)
			api := request(h, method, "/api/v1/inbox/"+got.Name, nil, true)
			if api.Code != http.StatusOK || page.Code != http.StatusOK || !bytes.Equal(api.Body.Bytes(), page.Body.Bytes()) {
				t.Fatalf("%s %s: API %d, Board %d", method, orig, api.Code, page.Code)
			}
			if method == "GET" && !bytes.Equal(api.Body.Bytes(), data) {
				t.Errorf("%s: bytes differ from the upload", orig)
			}
			for _, k := range []string{"Content-Type", "Content-Disposition", "Content-Security-Policy", "X-Content-Type-Options", "Cache-Control", "Content-Length", "Accept-Ranges"} {
				if api.Header().Get(k) != page.Header().Get(k) {
					t.Errorf("%s %s: %s is %q, the Board's is %q", method, orig, k, api.Header().Get(k), page.Header().Get(k))
				}
			}
		}
		if got.Kind == "file" {
			if h := request(h, "GET", "/api/v1/inbox/"+got.Name, nil, true).Header(); h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Security-Policy") != "sandbox; default-src 'none'" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment; filename=") {
				t.Errorf("%s: a file is served with %v", orig, h)
			}
		}
	}
	got := apiStored(t, h, "log.txt", []byte("0123456789"))
	req := httptest.NewRequest("GET", "/api/v1/inbox/"+got.Name, nil)
	req.Header.Set("Range", "bytes=2-4")
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" {
		t.Errorf("Range = %d %q", rec.Code, rec.Body)
	}
}

func TestAPIInboxNeedsTheSessionAndALoopbackBoard(t *testing.T) {
	fx := apiInboxFixture(t)
	got := apiStored(t, fx.handler(), "a.png", pngBytes)
	testAPIError(t, request(fx.handler(), "GET", "/api/v1/inbox/"+got.Name, nil, false), http.StatusUnauthorized, "unauthorized")
	fx.options.Controls = false
	rec := request(fx.handler(), "GET", "/api/v1/inbox/"+got.Name, nil, true)
	testAPIError(t, rec, http.StatusForbidden, "forbidden")
	if bytes.Contains(rec.Body.Bytes(), pngBytes) {
		t.Fatal("a network board served the bytes through the API")
	}
}

func TestAPIInboxServesOnlyInboxNames(t *testing.T) {
	fx := apiInboxFixture(t)
	h := fx.handler()
	home := fx.options.Home
	for _, f := range []string{"hand.db", "board.token"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := apiStored(t, h, "a.png", pngBytes)
	for _, f := range []string{".upload-123", ".hidden", "hand.db", "20261101-050000-1.html"} {
		if err := os.WriteFile(filepath.Join(home, "inbox", f), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const stamp = "20261101-050000-1-"
	probes := []struct {
		raw  string
		code int
	}{
		{got.Name, 200},
		{"..%2Fhand.db", 404}, {"%2E%2E%2Fhand.db", 404}, {"..%5Chand.db", 404}, {"hand.db", 404}, {"board.token", 404},
		{"%2Fetc%2Fpasswd", 404}, {url.PathEscape(filepath.Join(home, "hand.db")), 404}, {"C%3A%5CWindows%5Cwin.ini", 404},
		{"..", 307}, {".", 307}, {"%2e%2e", 404}, {"%2e", 404}, {"../hand.db", 307}, {"./" + got.Name, 307}, {got.Name + "/", 404}, {got.Name + "/.", 307},
		{stamp + "a%2F..%2F..%2Fhand.db", 404}, {stamp + "..%2Fhand.db", 404}, {stamp + "a%5C..%5Chand.db", 404},
		{stamp + "a.png%00", 404}, {stamp + "a%00.png", 404}, {"%00", 404},
		{"20261101-050000-1.png%2F..%2F..%2Fhand.db", 404}, {"20261101-050000-1.html", 404},
		{stamp + ".env", 404}, {stamp + "..hidden", 404}, {stamp + ".", 404}, {".upload-123", 404}, {".hidden", 404},
		{"20261101-050000-9-gone.png", 404}, {"20261101-050000-9.png", 404}, {"", 404},
	}
	for _, p := range probes {
		page := request(h, "GET", "/inbox/"+p.raw, nil, true)
		api := request(h, "GET", "/api/v1/inbox/"+p.raw, nil, true)
		if page.Code != p.code || api.Code != p.code {
			t.Errorf("%q: Board %d, API %d, the table says %d:\n%s", p.raw, page.Code, api.Code, p.code, api.Body)
			continue
		}
		if strings.Contains(api.Body.String(), secret) || strings.Contains(api.Body.String(), home) {
			t.Errorf("%q leaks:\n%s", p.raw, api.Body)
		}
		if p.code == 404 {
			testAPIError(t, api, 404, "not_found")
		}
	}
	const dir = "20261101-050000-8-dir.png"
	if err := os.Mkdir(filepath.Join(home, "inbox", dir), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/inbox/", "/api/v1/inbox/"} {
		if rec := request(h, "GET", p+dir, nil, true); rec.Code != http.StatusNotFound {
			t.Errorf("a folder in the inbox answered %s %d", p, rec.Code)
		}
	}
}

func TestAPIInboxNameRefusalNamesNoHostPath(t *testing.T) {
	fx := apiInboxFixture(t)
	rec := request(fx.handler(), "GET", "/api/v1/inbox/"+url.PathEscape(filepath.Join(fx.options.Home, "hand.db")), nil, true)
	testAPIError(t, rec, http.StatusNotFound, "not_found")
	if strings.Contains(rec.Body.String(), fx.options.Home) {
		t.Fatalf("the refusal names the host folder:\n%s", rec.Body)
	}
}

type sentTexts struct{ texts []string }

func (s *sentTexts) control(fx *fixture) {
	fx.options.Control = func(ctx context.Context, call ...string) (string, error) {
		if len(call) == 4 && call[2] == "--text" {
			s.texts = append(s.texts, call[3])
			_, err := fx.st.AddSupervisorInput(ctx, call[3])
			return "", err
		}
		return "", nil
	}
}

func pageComposerText(prose string, lines ...string) string {
	if prose != "" && !strings.HasSuffix(prose, "\n") {
		prose += "\n"
	}
	for _, l := range lines {
		prose += l + "\n"
	}
	return strings.ReplaceAll(prose, "\r\n", "\n")
}

func TestAPIMessageAttachmentsWriteTheLinesThePageWrites(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "(prose && !prose.endsWith(\"\\n\") ? `${prose}\\n` : prose) + sending.map((l) => `${l}\\n`).join(\"\")", "const l = r && `[${r.kind}: ${r.path}]`;")
	for _, c := range []struct {
		what, prose string
		files       []string
	}{
		{"prose and an image", "look at this", []string{"shot.png"}},
		{"prose ending in a newline", "look\n", []string{"shot.png"}},
		{"prose with CRLF", "one\r\ntwo", []string{"report.pdf"}},
		{"no prose", "", []string{"shot.png", "report.pdf"}},
		{"two files and prose", "both", []string{"report.pdf", "shot.png", "blob.bin"}},
		{"a file twice", "twice", []string{"shot.png", "shot.png"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			fx := apiInboxFixture(t)
			var sent sentTexts
			sent.control(fx)
			h := fx.handler()
			var lines, names []string
			for _, orig := range c.files {
				data := pngBytes
				if orig != "shot.png" {
					data = []byte(anyFiles[orig])
				}
				path, name, kind := saved(t, upload(h, orig, data, token))
				lines, names = append(lines, "["+kind+": "+path+"]"), append(names, name)
			}
			want := pageComposerText(c.prose, lines...)
			if res := post(h, "/supervisor/send", url.Values{"text": {want}}); res.StatusCode != http.StatusSeeOther {
				t.Fatalf("the page's send = %d", res.StatusCode)
			}
			body := map[string]any{"attachments": names}
			if c.prose != "" {
				body["text"] = c.prose
			}
			env, tl := apiBody[board.APITimeline](t, apiPost(t, h, "/api/v1/message", body, token, true))
			if len(sent.texts) != 2 || sent.texts[1] != sent.texts[0] || sent.texts[0] != want {
				t.Fatalf("the page sent %q, the API sent %q, the rule says %q", sent.texts[0], sent.texts[len(sent.texts)-1], want)
			}
			var carried []board.APIEntry
			for _, e := range tl.Entries {
				if e.Queued {
					carried = append(carried, e)
				}
			}
			if len(carried) != 2 {
				t.Fatalf("the answer holds %d queued messages: %+v", len(carried), tl.Entries)
			}
			for _, e := range carried {
				if len(e.Shots) != len(names) || e.Text != strings.TrimSpace(strings.ReplaceAll(c.prose, "\r\n", "\n")) {
					t.Fatalf("a queued message = %+v", e)
				}
				for i, s := range e.Shots {
					if s.Name != names[i] || s.Missing {
						t.Errorf("shot %d = %+v, want %s", i, s, names[i])
					}
				}
			}
			if strings.Contains(env.Fleet.ID, fx.options.Home) || bytes.Contains(mustJSON(t, tl), []byte(fx.options.Home)) {
				t.Errorf("the answer names the host folder")
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAPIMessageWithoutAttachmentsIsUnchanged(t *testing.T) {
	for _, body := range []map[string]any{
		{"text": "hello"}, {"text": "hello", "attachments": []string{}}, {"text": "hello", "attachments": nil},
	} {
		fx := apiInboxFixture(t)
		var sent sentTexts
		sent.control(fx)
		apiBody[board.APITimeline](t, apiPost(t, fx.handler(), "/api/v1/message", body, token, true))
		if len(sent.texts) != 1 || sent.texts[0] != "hello" {
			t.Fatalf("%v sent %q, want exactly hello", body, sent.texts)
		}
	}
	for _, body := range []string{`{}`, `{"text":""}`, `{"text":"  "}`, `{"attachments":[]}`, `{"text":"","attachments":[]}`, `{"text":null,"attachments":null}`} {
		fx := apiInboxFixture(t)
		var sent sentTexts
		sent.control(fx)
		testAPIError(t, apiPostRaw(t, fx.handler(), "/api/v1/message", body, token, true), http.StatusBadRequest, "bad_request")
		if len(sent.texts) != 0 {
			t.Fatalf("%s sent %q", body, sent.texts)
		}
	}
}

func TestAPIMessageRefusesAnAttachmentBeforeSendingAnything(t *testing.T) {
	fx := apiInboxFixture(t)
	var sent sentTexts
	sent.control(fx)
	h := fx.handler()
	good := apiStored(t, h, "shot.png", pngBytes).Name
	home := fx.options.Home
	if err := os.WriteFile(filepath.Join(home, "hand.db"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "inbox", "20261101-050000-5-dir.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what   string
		names  []string
		status int
		code   string
	}{
		{"a well-formed name that is not in the inbox", []string{"20261101-050000-9-gone.png"}, 404, "not_found"},
		{"a good name after a missing one", []string{good, "20261101-050000-9-gone.pdf"}, 404, "not_found"},
		{"a folder with an inbox name", []string{"20261101-050000-5-dir.png"}, 404, "not_found"},
		{"a bare file of the fleet folder", []string{"hand.db"}, 400, "bad_request"},
		{"the host path the page writes", []string{filepath.Join(home, "inbox", good)}, 400, "bad_request"},
		{"a path out of the inbox", []string{"../hand.db"}, 400, "bad_request"},
		{"a backslash path", []string{`..\hand.db`}, 400, "bad_request"},
		{"an encoded separator", []string{"20261101-050000-1-a%2F..%2Fhand.db"}, 400, "bad_request"},
		{"an absolute path", []string{"/etc/passwd"}, 400, "bad_request"},
		{"a NUL", []string{good + "\x00"}, 400, "bad_request"},
		{"a newline that would write a second line", []string{good + "]\n[file: /etc/passwd"}, 400, "bad_request"},
		{"a dot name", []string{"20261101-050000-1-.env"}, 400, "bad_request"},
		{"an empty name", []string{""}, 400, "bad_request"},
		{"a leading space", []string{" " + good}, 400, "bad_request"},
	} {
		rec := apiPost(t, h, "/api/v1/message", map[string]any{"text": "look", "attachments": c.names}, token, true)
		e := testAPIError(t, rec, c.status, c.code)
		if strings.Contains(e.Message, home) || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s: the refusal leaks: %q", c.what, e.Message)
		}
	}
	for _, raw := range []string{`{"text":"look","attachments":"x"}`, `{"text":"look","attachments":[1]}`, `{"text":"look","attachments":{"a":1}}`, `{"text":"look","attachments":[null]}`} {
		testAPIError(t, apiPostRaw(t, h, "/api/v1/message", raw, token, true), http.StatusBadRequest, "bad_request")
	}
	if len(sent.texts) != 0 {
		t.Fatalf("a refused message was still sent: %q", sent.texts)
	}
	apiBody[board.APITimeline](t, apiPost(t, h, "/api/v1/message", map[string]any{"text": "look", "attachments": []string{good}}, token, true))
	if len(sent.texts) != 1 {
		t.Fatalf("the good message was sent %d times", len(sent.texts))
	}
}

func TestAPIMessageAttachmentsKeepTheMessageLimits(t *testing.T) {
	fx := apiInboxFixture(t)
	var sent sentTexts
	sent.control(fx)
	h := fx.handler()
	name := apiStored(t, h, "shot.png", pngBytes).Name
	testAPIError(t, apiPost(t, h, "/api/v1/message", map[string]any{"text": strings.Repeat("x", 16<<10), "attachments": []string{name}}, token, true), http.StatusBadRequest, "bad_request")
	testAPIError(t, apiPost(t, h, "/api/v1/message", map[string]any{"text": "x", "attachments": []string{name}}, "", true), http.StatusForbidden, "csrf_required")
	fx.options.Control = nil
	testAPIError(t, apiPost(t, fx.handler(), "/api/v1/message", map[string]any{"text": "x", "attachments": []string{name}}, token, true), http.StatusForbidden, "forbidden")
	if len(sent.texts) != 0 {
		t.Fatalf("sent %q", sent.texts)
	}
}

func TestAPIMessageOfOnlyAttachmentsIsTheLinesAlone(t *testing.T) {
	fx := apiInboxFixture(t)
	var sent sentTexts
	sent.control(fx)
	h := fx.handler()
	name := apiStored(t, h, "shot.png", pngBytes).Name
	apiBody[board.APITimeline](t, apiPost(t, h, "/api/v1/message", map[string]any{"attachments": []string{name}}, token, true))
	want := "[image: " + filepath.Join(fx.options.Home, "inbox", name) + "]\n"
	if len(sent.texts) != 1 || sent.texts[0] != want {
		t.Fatalf("sent %q, want %q", sent.texts, want)
	}
}

func TestThePhoneDecodesTheInboxAnswersWithoutAChange(t *testing.T) {
	envelope := kotlinParamsIn(t, "BoardClient.kt", "class Envelope<T>(")
	var required []string
	for _, p := range envelope {
		if p = strings.TrimSpace(p); p != "" && !strings.Contains(p, " = ") {
			required = append(required, kotlinValName.FindStringSubmatch(p)[1])
		}
	}
	status := phoneRequired(t, "Status")
	raw, err := os.ReadFile(filepath.Join("testdata", "api", "inbox-stored.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for _, k := range required {
		if _, ok := root[k]; !ok {
			t.Errorf("inbox-stored has no %s, which the phone's Envelope requires", k)
		}
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(root["data"], &data); err != nil {
		t.Fatal(err)
	}
	for _, k := range status {
		if _, ok := data[k]; ok {
			t.Errorf("inbox-stored carries %s, a field of the phone's Status", k)
		}
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "api", "message-sent.json"))
	if err != nil {
		t.Fatal(err)
	}
	phone, err := os.ReadFile(filepath.Join("..", "..", "android", "src-tauri", "gen", "android", "app", "src", "test", "resources", "api", "message-sent.json"))
	if err != nil || !bytes.Equal(golden, phone) {
		t.Fatalf("the phone's copy of message-sent.json differs from the golden (%v)", err)
	}
}

func TestAPIInboxConcurrentUploadsKeepEveryFile(t *testing.T) {
	for _, c := range []struct {
		what    string
		viaPage func(i int) bool
	}{
		{"through the API", func(int) bool { return false }},
		{"through the API and the page", func(i int) bool { return i%2 == 1 }},
	} {
		t.Run(c.what, func(t *testing.T) {
			fx := apiInboxFixture(t)
			h := fx.handler()
			const n = 24
			names := make([]string, n)
			var wg sync.WaitGroup
			for i := range names {
				wg.Go(func() {
					data := append(append([]byte{}, pngBytes...), byte(i))
					if c.viaPage(i) {
						_, names[i], _ = saved(t, upload(h, "a.png", data, token))
						return
					}
					rec := apiUpload(h, "file", "a.png", data, token, true)
					if rec.Code != http.StatusOK {
						t.Errorf("upload %d = %d:\n%s", i, rec.Code, rec.Body)
						return
					}
					_, got := apiBody[board.APIInboxFile](t, rec)
					names[i] = got.Name
				})
			}
			wg.Wait()
			seen := map[string]bool{}
			for i, name := range names {
				if name == "" || seen[name] {
					t.Fatalf("upload %d got the name %q, which is empty or taken", i, name)
				}
				seen[name] = true
				b, err := os.ReadFile(filepath.Join(fx.options.Home, "inbox", name))
				if err != nil || len(b) != len(pngBytes)+1 || b[len(b)-1] != byte(i) {
					t.Fatalf("%s does not hold upload %d (%v, %d bytes)", name, i, err, len(b))
				}
			}
			if got := inboxFiles(t, fx.options.Home); len(got) != n {
				t.Fatalf("inbox = %q, want %d files and no .upload-* leftovers", got, n)
			}
		})
	}
}
