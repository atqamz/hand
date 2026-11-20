package board_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
)

func linksFiles() map[string]string {
	return map[string]string{
		"reports/t1/design.md":   design,
		"reports/t1/notes.txt":   "one\n",
		"reports/t1/flow.png":    string(flowPNG),
		"reports/t1/.dot/h.md":   "hidden\n",
		"reports/t1/sub/x.txt":   "x\n",
		"reports/t1/a b.md":      "spaced\n",
		"backups/b.md":           "backup\n",
		"top.md":                 "top\n",
		"hand.db":                secret,
		"locks/watch.lock":       secret,
		"projects/app/README.md": secret,
	}
}

func linksQuery(paths ...string) string {
	v := url.Values{"path": paths}
	return "/api/v1/links?" + v.Encode()
}

func linksOf(t *testing.T, h http.Handler, paths ...string) board.APILinks {
	t.Helper()
	_, got := apiInto[board.APILinks](t, h, linksQuery(paths...))
	if len(got.Links) != len(paths) {
		t.Fatalf("%d paths, %d answers: %+v", len(paths), len(got.Links), got.Links)
	}
	return got
}

func linksParity(t *testing.T, h http.Handler, home string, paths []string) {
	t.Helper()
	got := linksOf(t, h, paths...)
	for i, p := range paths {
		want := board.DocHref(home, "", p)
		switch l := got.Links[i]; {
		case want == "" && l != nil:
			t.Errorf("%q: DocHref refuses, the route answers %+v", p, *l)
		case want != "" && l == nil:
			t.Errorf("%q: DocHref gives %q, the route answers null", p, want)
		case l != nil && l.Href != want:
			t.Errorf("%q: href = %q, DocHref = %q", p, l.Href, want)
		}
	}
}

func TestAPILinksAnswersWhatDocHrefAnswers(t *testing.T) {
	h, home := apiDocBoard(t, linksFiles())
	t.Setenv("HOME", filepath.Dir(home))
	t.Setenv("USERPROFILE", filepath.Dir(home))
	linked := map[string]board.APILink{
		"reports/t1/design.md":                               {Rel: "reports/t1/design.md", Href: "/doc/reports/t1/design.md"},
		"reports/t1/a b.md":                                  {Rel: "reports/t1/a b.md", Href: "/doc/reports/t1/a%20b.md"},
		"./reports/t1/design.md":                             {Rel: "reports/t1/design.md", Href: "/doc/reports/t1/design.md"},
		"reports/t1/sub":                                     {Rel: "reports/t1/sub", Href: "/doc/reports/t1/sub", Dir: true},
		"reports/":                                           {Rel: "reports", Href: "/doc/reports", Dir: true},
		"reports/t1/flow.png":                                {Rel: "reports/t1/flow.png", Href: "/doc/reports/t1/flow.png"},
		filepath.Join(home, "reports/t1/notes.txt"):          {Rel: "reports/t1/notes.txt", Href: "/doc/reports/t1/notes.txt"},
		filepath.Join(home, "reports", "t1") + "/":           {Rel: "reports/t1", Href: "/doc/reports/t1", Dir: true},
		"~/" + filepath.Base(home) + "/reports/t1/notes.txt": {Rel: "reports/t1/notes.txt", Href: "/doc/reports/t1/notes.txt"},
	}
	var paths []string
	for p := range linked {
		paths = append(paths, p)
	}
	got := linksOf(t, h, paths...)
	for i, p := range paths {
		if l := got.Links[i]; l == nil || *l != linked[p] {
			t.Errorf("%q = %+v, want %+v", p, l, linked[p])
		}
	}
	refused := []string{
		"", "top.md", "hand.db", "reports/missing.md", "reports/t1/design.md/x", "reports/../top.md", "../top.md", "reports/t1/..",
		"reports/t1/.dot/h.md", "reports/t1/.dot", ".", "./", "backups/b.md", "backups", "locks/watch.lock", "projects/app/README.md",
		"cache", "luvus", "reports/t1/design.md:12", "reports/t1/design.md#L3",
		"/etc/passwd", "/", "~/docs/h.md", "~/", "~", "//host/share/x", `\\host\share\x`, `C:\Users\x\a.md`, "C:/Users/x/a.md",
		`reports\t1\design.md`, filepath.Join(home, "top.md"), filepath.Join(home, "hand.db"), filepath.Join(home, "backups", "b.md"),
		filepath.Join(filepath.Dir(home), "elsewhere", "reports", "t1", "notes.txt"), "$FLEET/top.md",
	}
	got = linksOf(t, h, refused...)
	for i, p := range refused {
		if got.Links[i] != nil {
			t.Errorf("%q = %+v, want null", p, *got.Links[i])
		}
	}
	linksParity(t, h, home, append(append(paths, refused...), "reports/t1/a b.md", "reports/t1/design.md"))
}

func TestAPILinksKeepsOrderAndLength(t *testing.T) {
	h, _ := apiDocBoard(t, linksFiles())
	got := linksOf(t, h, "reports/t1/notes.txt", "top.md", "reports/t1/design.md", "reports/t1/notes.txt", "reports")
	rels := make([]string, len(got.Links))
	for i, l := range got.Links {
		if l != nil {
			rels[i] = l.Rel
		}
	}
	if strings.Join(rels, "|") != "reports/t1/notes.txt||reports/t1/design.md|reports/t1/notes.txt|reports" || got.Limit != 50 {
		t.Fatalf("answers = %q, limit %d", rels, got.Limit)
	}
	rec := request(h, "GET", linksQuery("top.md"), nil, true)
	var raw struct {
		Data struct{ Links []json.RawMessage }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw.Data.Links) != 1 || string(raw.Data.Links[0]) != "null" {
		t.Fatalf("a refusal is not JSON null (%v): %s", err, rec.Body)
	}
}

func TestAPILinksTakesFiftyPathsAndNoMore(t *testing.T) {
	h, _ := apiDocBoard(t, linksFiles())
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "reports/t1/notes.txt"
		}
		return out
	}
	if got := linksOf(t, h, many(50)...); got.Links[49] == nil {
		t.Fatalf("50th = %+v", got.Links[49])
	}
	long := strings.Repeat("a", 512)
	if got := linksOf(t, h, long); got.Links[0] != nil {
		t.Fatalf("512 bytes = %+v", got.Links[0])
	}
	spread := make([]string, 50)
	for i := range spread {
		spread[i] = strings.Repeat("b", 330)
	}
	over := linksQuery(spread...)
	if len(over) <= 16<<10 {
		t.Fatalf("test query is %d bytes", len(over))
	}
	for name, path := range map[string]string{
		"no path":         "/api/v1/links",
		"other key":       "/api/v1/links?p=top.md",
		"51 paths":        linksQuery(many(51)...),
		"513 bytes":       linksQuery(long + "a"),
		"a NUL":           "/api/v1/links?path=reports%2Ft1%2Fnotes.txt%00",
		"a NUL alone":     "/api/v1/links?path=%00",
		"over 16 KiB":     over,
		"a bad escape":    "/api/v1/links?path=%zz",
		"one over the 16": "/api/v1/links?path=a&x=" + strings.Repeat("y", 16<<10),
	} {
		err := testAPIError(t, request(h, "GET", path, nil, true), http.StatusBadRequest, "bad_request")
		if strings.Contains(err.Message, "notes.txt") || len(err.Message) > 200 {
			t.Errorf("%s: message echoes input: %q", name, err.Message)
		}
	}
}

func TestAPILinksNeverReturnsAHostPath(t *testing.T) {
	h, home := apiDocBoard(t, linksFiles())
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{filepath.Join(home, "reports/t1/design.md"), filepath.Join(real, "reports/t1/notes.txt"), filepath.Join(home, "top.md"), filepath.Join(home, "reports"), filepath.Join(home, "nope")}
	for _, path := range []string{linksQuery(inputs...), linksQuery(strings.Repeat("x", 600), filepath.Join(home, "top.md"))} {
		rec := request(h, "GET", path, nil, true)
		for _, bad := range []string{home, real, filepath.ToSlash(home), url.QueryEscape(home), filepath.Base(home)} {
			if strings.Contains(rec.Body.String(), bad) {
				t.Errorf("body names %q:\n%s", bad, rec.Body)
			}
		}
	}
	got := linksOf(t, h, inputs...)
	for i, l := range got.Links {
		if l != nil && (filepath.IsAbs(l.Rel) || strings.HasPrefix(l.Rel, "/") || strings.Contains(l.Rel, "..")) {
			t.Errorf("%q: rel = %q", inputs[i], l.Rel)
		}
	}
}

func TestAPILinksNeedsTheSessionAsStatusDoes(t *testing.T) {
	h, _ := apiDocBoard(t, linksFiles())
	want := request(h, "GET", "/api/v1/status", nil, false)
	got := request(h, "GET", linksQuery("reports/t1/design.md"), nil, false)
	if got.Code != want.Code || got.Code != http.StatusUnauthorized {
		t.Fatalf("links without a cookie = %d, status = %d", got.Code, want.Code)
	}
	testAPIError(t, got, http.StatusUnauthorized, "unauthorized")
	testAPIError(t, request(h, "GET", linksQuery("reports/t1/design.md")+"&token="+token, nil, false), http.StatusUnauthorized, "unauthorized")
	req := httptest.NewRequest("GET", linksQuery("reports/t1/design.md"), nil)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	status := httptest.NewRequest("GET", "/api/v1/status", nil)
	status.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	status.Header.Set("X-Forwarded-For", "203.0.113.9")
	srec := httptest.NewRecorder()
	h.ServeHTTP(srec, status)
	if rec.Code != srec.Code || rec.Code != http.StatusForbidden {
		t.Fatalf("a proxied links = %d, status = %d", rec.Code, srec.Code)
	}
}

func TestAPILinksReadsLikeEveryOtherGet(t *testing.T) {
	h, home := apiDocBoard(t, linksFiles())
	path := linksQuery("reports/t1/notes.txt")
	rec := request(h, "GET", path, nil, true)
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("headers = %v", rec.Header())
	}
	cursor := rec.Header().Get("X-Hand-Cursor")
	if cursor == "" {
		t.Fatal("no cursor")
	}
	if head := request(h, "HEAD", path, nil, true); head.Code != http.StatusOK {
		t.Errorf("HEAD = %d", head.Code)
	}
	if post := request(h, "POST", path, url.Values{"csrf": {token}}, true); post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST = %d, Allow %q", post.Code, post.Header().Get("Allow"))
	}
	if same := request(h, "GET", path+"&since="+cursor, nil, true); same.Code != http.StatusNotModified {
		t.Errorf("same answer with since = %d", same.Code)
	}
	if err := os.Remove(filepath.Join(home, "reports/t1/notes.txt")); err != nil {
		t.Fatal(err)
	}
	if moved := request(h, "GET", path+"&since="+cursor, nil, true); moved.Code != http.StatusOK || !strings.Contains(moved.Body.String(), `"links":[null]`) {
		t.Errorf("after the file is gone = %d:\n%s", moved.Code, moved.Body)
	}
	testAPIError(t, request(h, "GET", path+"&since=nope", nil, true), http.StatusBadRequest, "bad_cursor")
}

func TestAPILinksAnswersWithTheBoardsBase(t *testing.T) {
	home := fleetFolder(t, linksFiles())
	h := board.New(open(t), token, board.Options{Home: home, Base: "/f00000000e0aa", Controls: true})
	req := httptest.NewRequest("GET", linksQuery("reports/t1/design.md", "top.md", "reports"), nil)
	req.AddCookie(&http.Cookie{Name: "hand_board_f00000000e0aa", Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env struct{ Data board.APILinks }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("links = %d (%v): %.200s", rec.Code, err, rec.Body)
	}
	got := env.Data
	want := board.DocHref(home, "/f00000000e0aa", "reports/t1/design.md")
	if got.Links[0] == nil || got.Links[0].Href != want || want != "/f00000000e0aa/doc/reports/t1/design.md" || got.Links[1] != nil || got.Links[2] == nil || !got.Links[2].Dir {
		t.Fatalf("links = %+v, want %q", got.Links, want)
	}
}

func TestAPIRoutesIncludeLinksAndTheDocsNameIt(t *testing.T) {
	var found bool
	for _, rt := range board.APIRoutes() {
		found = found || rt.Method == "GET" && rt.Path == "/links"
	}
	if !found {
		t.Fatal("the API table lacks GET /links")
	}
	doc, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "| `GET /api/v1/links?path=PATH[&path=PATH...]` |") {
		t.Error("docs/api.md has no endpoint row for GET /api/v1/links")
	}
}
