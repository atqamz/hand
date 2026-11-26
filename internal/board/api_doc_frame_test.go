package board_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const pdfPolicy = "default-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

func TestAPIDocFrameInlinesImagesUnderTheBoardsFrameHeaders(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	want := request(h, "GET", "/doc/reports/t1/page.html?frame", nil, true)
	if !strings.Contains(want.Body.String(), "data:image/png;base64,") {
		t.Fatalf("the Board's frame no longer inlines pictures, so this test pins nothing:\n%s", want.Body)
	}
	for _, q := range []string{"?raw&frame", "?frame&raw", "?raw&frame=1"} {
		rec := request(h, "GET", "/api/v1/doc/reports/t1/page.html"+q, nil, true)
		if rec.Code != http.StatusOK || rec.Body.String() != want.Body.String() {
			t.Errorf("%s = %d:\n%s", q, rec.Code, rec.Body)
		}
		headers(t, q, rec, map[string]string{
			"Content-Type":            "text/html; charset=utf-8",
			"Content-Security-Policy": framedCSP,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "SAMEORIGIN",
			"Cache-Control":           "private, no-cache",
		})
	}
}

func TestAPIDocFrameTakesRangesAndHead(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	full := request(h, "GET", "/api/v1/doc/reports/t1/page.html?raw&frame", nil, true).Body.String()
	head := request(h, "HEAD", "/api/v1/doc/reports/t1/page.html?raw&frame", nil, true)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(full)) {
		t.Errorf("head = %d, %d bytes, length %q, body is %d", head.Code, head.Body.Len(), head.Header().Get("Content-Length"), len(full))
	}
	req := httptest.NewRequest("GET", "/api/v1/doc/reports/t1/page.html?raw&frame", nil)
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != full[:10] || rec.Header().Get("Content-Range") != "bytes 0-9/"+strconv.Itoa(len(full)) {
		t.Errorf("range = %d %q %q, want 206 %q of %d", rec.Code, rec.Body, rec.Header().Get("Content-Range"), full[:10], len(full))
	}
}

func TestAPIDocFrameStopsAtTheWholeFileCap(t *testing.T) {
	const cap = 8 << 20
	page := func(n int) string {
		p := `<h1>Hi</h1><img src="flow.png">`
		return p + strings.Repeat(" ", n-len(p))
	}
	for _, c := range []struct {
		name   string
		size   int
		inline bool
	}{{"at the cap", cap, true}, {"one byte over", cap + 1, false}} {
		t.Run(c.name, func(t *testing.T) {
			files := docFiles()
			files["reports/t1/big.html"] = page(c.size)
			h, _ := apiDocBoard(t, files)
			rec := request(h, "GET", "/api/v1/doc/reports/t1/big.html?raw&frame", nil, true)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data:image") != c.inline {
				t.Fatalf("%d bytes: %d, inlined %v, want %v", c.size, rec.Code, strings.Contains(rec.Body.String(), "data:image"), c.inline)
			}
			if !c.inline && rec.Body.String() != files["reports/t1/big.html"] {
				t.Errorf("%d bytes: the page changed", c.size)
			}
			headers(t, c.name, rec, map[string]string{"Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": framedCSP})
		})
	}
}

func TestAPIDocFrameIsIgnoredForOtherKinds(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	for _, name := range []string{"design.md", "flow.png", "logo.svg", "data.csv", "notes.txt", "spec.pdf", "blob.bin", "clip.mp4", "song.mp3"} {
		t.Run(name, func(t *testing.T) {
			want := request(h, "GET", "/api/v1/doc/reports/t1/"+name+"?raw", nil, true)
			got := request(h, "GET", "/api/v1/doc/reports/t1/"+name+"?raw&frame", nil, true)
			if got.Code != http.StatusOK || got.Body.String() != want.Body.String() || got.Body.String() != docFiles()["reports/t1/"+name] {
				t.Fatalf("%s: %d %q, want %q", name, got.Code, got.Body, want.Body)
			}
			for _, k := range []string{"Content-Type", "Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Content-Disposition", "Cache-Control"} {
				if got.Header().Get(k) != want.Header().Get(k) {
					t.Errorf("%s: %s = %q with frame, %q without", name, k, got.Header().Get(k), want.Header().Get(k))
				}
			}
		})
	}
}

func TestAPIDocPDFHasNoSandboxAndEveryOtherKindKeepsIt(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	rec := request(h, "GET", "/api/v1/doc/reports/t1/spec.pdf?raw", nil, true)
	headers(t, "pdf", rec, map[string]string{
		"Content-Type":            "application/pdf",
		"Content-Security-Policy": pdfPolicy,
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "SAMEORIGIN",
		"Cache-Control":           "private, no-cache",
		"Content-Disposition":     "",
	})
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("pdf policy %q has sandbox", rec.Header().Get("Content-Security-Policy"))
	}
	for _, name := range []string{"design.md", "flow.png", "logo.svg", "page.html", "data.csv", "notes.txt", "blob.bin", "clip.mp4", "song.mp3"} {
		if csp := request(h, "GET", "/api/v1/doc/reports/t1/"+name+"?raw", nil, true).Header().Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox; default-src 'none'") {
			t.Errorf("%s policy %q lost sandbox", name, csp)
		}
	}
	fake := docFiles()
	fake["reports/t1/fake.pdf"] = "not a pdf\n"
	h, _ = apiDocBoard(t, fake)
	rec = request(h, "GET", "/api/v1/doc/reports/t1/fake.pdf?raw", nil, true)
	if rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox;") {
		t.Errorf("a text file named .pdf = %q under %q", rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
	}
}

func TestTheWebPDFKeepsItsSandbox(t *testing.T) {
	h, _ := apiDocBoard(t, docFiles())
	rec := request(h, "GET", "/doc/reports/t1/spec.pdf?raw", nil, true)
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox; default-src 'none'" {
		t.Errorf("web pdf policy = %q", got)
	}
}

func TestAPIDocFrameRefusesWhatRawRefuses(t *testing.T) {
	files := docFiles()
	files["reports/t1/.draft.html"] = `<img src="flow.png">`
	files["hand.db"] = secret
	h, _ := apiDocBoard(t, files)
	for _, q := range []string{"?raw&frame", "?frame&raw"} {
		for rel, code := range map[string]int{
			"..%2F..%2Fhand.db": http.StatusForbidden, "reports/t1/..%2F..%2Fhand.db": http.StatusForbidden, "reports%5C..%5Chand.db": http.StatusForbidden,
			"hand.db": http.StatusForbidden, "reports/t1/.draft.html": http.StatusForbidden, "reports/t1/missing.html": http.StatusNotFound,
		} {
			rec := request(h, "GET", "/api/v1/doc/"+rel+q, nil, true)
			if rec.Code != code || strings.Contains(rec.Body.String(), "data:image") || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s%s = %d, want %d:\n%s", rel, q, rec.Code, code, rec.Body)
			}
		}
		for _, name := range []string{"page.html", "spec.pdf"} {
			rec := request(h, "GET", "/api/v1/doc/reports/t1/"+name+q, nil, false)
			testAPIError(t, rec, http.StatusUnauthorized, "unauthorized")
			if rec.Header().Get("Content-Security-Policy") == pdfPolicy || strings.Contains(rec.Body.String(), "data:image") {
				t.Errorf("%s%s without the cookie leaked: %q", name, q, rec.Body)
			}
		}
	}
}

func TestAPIDocFrameIsIgnoredForAnSVGWithAnImage(t *testing.T) {
	svg := `<img src="flow.png"><svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`
	files := docFiles()
	files["reports/t1/pic.svg"] = svg
	h, _ := apiDocBoard(t, files)
	for _, q := range []string{"?raw", "?raw&frame"} {
		rec := request(h, "GET", "/api/v1/doc/reports/t1/pic.svg"+q, nil, true)
		if rec.Body.String() != svg || rec.Header().Get("Content-Type") != "image/svg+xml" {
			t.Errorf("%s = %q %q", q, rec.Header().Get("Content-Type"), rec.Body)
		}
	}
}

func TestAPIDocPDFPolicyFollowsTheNameInAnyCaseAndOnlyIt(t *testing.T) {
	files := docFiles()
	for _, rel := range []string{"up.PDF", "mixed.PdF"} {
		files["reports/t1/"+rel] = pdfBytes
	}
	for _, rel := range []string{"x.txt", "x.html", "x.htm", "x.svg", "x.pdf.html", "x.pdfx", "x.pdf.txt"} {
		files["reports/t1/"+rel] = pdfBytes + "<script>1</script>"
	}
	h, _ := apiDocBoard(t, files)
	for _, rel := range []string{"up.PDF", "mixed.PdF"} {
		rec := request(h, "GET", "/api/v1/doc/reports/t1/"+rel+"?raw", nil, true)
		headers(t, rel, rec, map[string]string{"Content-Type": "application/pdf", "Content-Security-Policy": pdfPolicy})
	}
	for _, rel := range []string{"x.txt", "x.html", "x.htm", "x.svg", "x.pdf.html", "x.pdfx", "x.pdf.txt"} {
		for _, q := range []string{"?raw", "?raw&frame"} {
			rec := request(h, "GET", "/api/v1/doc/reports/t1/"+rel+q, nil, true)
			if rec.Header().Get("Content-Type") == "application/pdf" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox;") {
				t.Errorf("%s%s = %q under %q", rel, q, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Security-Policy"))
			}
		}
	}
}
