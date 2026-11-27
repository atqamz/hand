package webhost_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/atqamz/hand/internal/desktop/webhost"
)

type cookie struct{ name, value, domain string }

type fakeSession struct {
	id      int
	cookies []cookie
}

func (s *fakeSession) SetCookie(name, value, domain string) error {
	s.cookies = append(s.cookies, cookie{name, value, domain})
	return nil
}

type fakeView struct {
	session   *fakeSession
	loaded    []string
	pages     []string
	bases     []string
	failed    func()
	navigate  func(string) bool
	download  func(string) bool
	window    func(string) bool
	permit    func(string) bool
	origins   []string
	originErr error
	gone      func()
	order     []string
	destroyed int
}

func (v *fakeView) Native() ui.NativeWebView { return nil }
func (v *fakeView) Load(u string)            { v.loaded = append(v.loaded, u); v.order = append(v.order, "load") }
func (v *fakeView) LoadHTML(page, base string) {
	v.pages, v.bases = append(v.pages, page), append(v.bases, base)
	v.order = append(v.order, "html")
}
func (v *fakeView) OnFailed(fn func())                        { v.failed = fn }
func (v *fakeView) WillNavigate(allow func(string) bool)      { v.navigate = allow }
func (v *fakeView) WillDownload(allow func(string) bool)      { v.download = allow }
func (v *fakeView) WillOpenWindow(allow func(string) bool)    { v.window = allow }
func (v *fakeView) WillUsePermission(allow func(string) bool) { v.permit = allow }
func (v *fakeView) OnGone(fn func())                          { v.gone = fn }
func (v *fakeView) Destroy()                                  { v.destroyed++ }

func (v *fakeView) OnlyOrigin(o string) error {
	v.origins = append(v.origins, o)
	v.order = append(v.order, "origin")
	return v.originErr
}

type fakeEngine struct {
	mu        sync.Mutex
	sessions  []*fakeSession
	views     []*fakeView
	sessErr   error
	viewErr   error
	originErr error
}

func (e *fakeEngine) NewSession() (webhost.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessErr != nil {
		return nil, e.sessErr
	}
	s := &fakeSession{id: len(e.sessions)}
	e.sessions = append(e.sessions, s)
	return s, nil
}

func (e *fakeEngine) NewView(s webhost.Session) (webhost.View, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.viewErr != nil {
		return nil, e.viewErr
	}
	v := &fakeView{session: s.(*fakeSession), originErr: e.originErr}
	e.views = append(e.views, v)
	return v, nil
}

func frame(host, domain, token string) desktop.DocFrame {
	return desktop.DocFrame{
		Host:   host,
		URL:    "http://" + domain + ":4100/f0/api/v1/doc/reports/plan.pdf?raw",
		Cookie: desktop.WebCookie{Name: "hand_board_f0", Value: token, Domain: domain},
	}
}

func TestEachHostGetsOneSessionWithItsOwnCookieOnly(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	if _, err := h.Open(frame("local", "127.0.0.1", "local-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Open(frame("peer-1", "127.0.0.2", "peer-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Open(frame("local", "127.0.0.1", "local-token-2")); err != nil {
		t.Fatal(err)
	}
	if len(e.sessions) != 2 {
		t.Fatalf("sessions = %d, want one per host", len(e.sessions))
	}
	if got := e.sessions[0].cookies; len(got) != 2 || got[0] != (cookie{"hand_board_f0", "local-token", "127.0.0.1"}) || got[1].value != "local-token-2" {
		t.Fatalf("local session cookies = %v", got)
	}
	if got := e.sessions[1].cookies; len(got) != 1 || got[0] != (cookie{"hand_board_f0", "peer-token", "127.0.0.2"}) {
		t.Fatalf("peer session cookies = %v", got)
	}
	for i, v := range e.views {
		want := []*fakeSession{e.sessions[0], e.sessions[1], e.sessions[0]}[i]
		if v.session != want {
			t.Fatalf("view %d uses session %d, want %d", i, v.session.id, want.id)
		}
	}
}

func TestACookieIsNeverSentToAnAddressOfAnotherHost(t *testing.T) {
	for name, f := range map[string]desktop.DocFrame{
		"domain differs": {Host: "local", URL: "http://203.0.113.9:4100/f0/api/v1/doc/a.pdf?raw", Cookie: desktop.WebCookie{Name: "c", Value: "secret", Domain: "127.0.0.1"}},
		"https":          {Host: "local", URL: "https://127.0.0.1:4100/f0/api/v1/doc/a.pdf?raw", Cookie: desktop.WebCookie{Name: "c", Value: "secret", Domain: "127.0.0.1"}},
		"file":           {Host: "local", URL: "file:///etc/passwd", Cookie: desktop.WebCookie{Name: "c", Value: "secret", Domain: ""}},
		"no host id":     {URL: "http://127.0.0.1:4100/f0/api/v1/doc/a.pdf?raw", Cookie: desktop.WebCookie{Name: "c", Value: "secret", Domain: "127.0.0.1"}},
		"no address":     {Host: "local", Cookie: desktop.WebCookie{Name: "c", Value: "secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeEngine{}
			h := &webhost.Host{Engine: e}
			if _, err := h.Open(f); err == nil {
				t.Fatal("opened a frame that is not one address of one host")
			}
			for _, s := range e.sessions {
				if len(s.cookies) != 0 {
					t.Fatalf("a cookie was set: %v", s.cookies)
				}
			}
			if len(e.views) != 0 {
				t.Fatal("a view was created")
			}
		})
	}
}

func TestAViewLoadsOnlyTheFileAndDeniesEverythingElse(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	f := frame("local", "127.0.0.1", "t")
	handle, err := h.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	v := e.views[0]
	if len(v.loaded) != 1 || v.loaded[0] != f.URL {
		t.Fatalf("loaded = %v, want only %q", v.loaded, f.URL)
	}
	if !v.navigate(f.URL) {
		t.Fatal("the file's own address is denied")
	}
	for _, to := range []string{
		"http://127.0.0.1:4100/f0/api/v1/doc/reports/other.pdf?raw",
		"http://127.0.0.1:4100/f0/api/v1/doc/reports/plan.pdf",
		"http://127.0.0.1:4100/f1/api/v1/status",
		"http://203.0.113.9/",
		"https://example.com/",
		"file:///etc/passwd",
		"about:blank",
		"",
		f.URL + "&download=1",
		f.URL + "x",
		f.URL + "#page=2",
		f.URL + "/../other",
	} {
		if v.navigate(to) {
			t.Errorf("navigation to %q is allowed", to)
		}
	}
	if v.download("http://127.0.0.1:4100/x") || v.download(f.URL) {
		t.Error("a download is allowed")
	}
	if v.window(f.URL) || v.window("https://example.com/") {
		t.Error("a new window is allowed")
	}
	if v.permit("http://127.0.0.1:4100") || v.permit("") {
		t.Error("a permission is granted")
	}
	if handle.Close == nil {
		t.Fatal("no close")
	}
	handle.Close()
	if v.destroyed != 1 {
		t.Fatalf("destroyed = %d, want 1", v.destroyed)
	}
}

func TestAMissingEngineIsAnErrorNotACrash(t *testing.T) {
	for name, e := range map[string]*fakeEngine{
		"no view":    {viewErr: errors.New("libwebkit2gtk is not installed")},
		"no session": {sessErr: errors.New("no session")},
	} {
		t.Run(name, func(t *testing.T) {
			h := &webhost.Host{Engine: e}
			handle, err := h.Open(frame("local", "127.0.0.1", "t"))
			if err == nil {
				t.Fatal("no error")
			}
			if handle.View != nil || handle.Close != nil {
				t.Fatal("a handle came with the error")
			}
		})
	}
}

func TestAViewIsConfinedToTheBoardsOriginBeforeItLoads(t *testing.T) {
	for name, tc := range map[string]struct{ url, domain, origin string }{
		"port":         {"http://127.0.0.1:4100/f0/api/v1/doc/a.html?raw&frame", "127.0.0.1", "http://127.0.0.1:4100"},
		"default port": {"http://127.0.0.1:80/f0/api/v1/doc/a.html?raw&frame", "127.0.0.1", "http://127.0.0.1"},
		"no port":      {"http://localhost/f0/api/v1/doc/a.html?raw&frame", "localhost", "http://localhost"},
		"other port":   {"http://localhost:8080/f0/api/v1/doc/a.html?raw&frame", "localhost", "http://localhost:8080"},
		"IPv6":         {"http://[::1]:4100/f0/api/v1/doc/a.html?raw&frame", "::1", "http://[::1]:4100"},
		"IPv6 port 80": {"http://[::1]:80/f0/api/v1/doc/a.html?raw&frame", "::1", "http://[::1]"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeEngine{}
			h := &webhost.Host{Engine: e}
			f := desktop.DocFrame{Host: "local", URL: tc.url, Cookie: desktop.WebCookie{Name: "c", Value: "t", Domain: tc.domain}}
			if _, err := h.Open(f); err != nil {
				t.Fatal(err)
			}
			v := e.views[0]
			if len(v.origins) != 1 || v.origins[0] != tc.origin {
				t.Fatalf("origins = %v, want [%s]", v.origins, tc.origin)
			}
			if len(v.order) != 2 || v.order[0] != "origin" || v.order[1] != "load" {
				t.Fatalf("order = %v, want the origin set before the load", v.order)
			}
		})
	}
}

func TestAViewThatCannotBeConfinedIsDestroyedAndNeverLoads(t *testing.T) {
	e := &fakeEngine{originErr: errors.New("content rules are not available")}
	h := &webhost.Host{Engine: e}
	handle, err := h.Open(frame("local", "127.0.0.1", "t"))
	if err == nil {
		t.Fatal("opened a view that is not confined")
	}
	if !errors.Is(err, desktop.ErrWebUnconfined) || !errors.Is(err, e.originErr) {
		t.Fatalf("err = %v, want ErrWebUnconfined wrapping the cause", err)
	}
	if handle.View != nil || handle.Close != nil || handle.OnGone != nil {
		t.Fatal("a handle came with the error")
	}
	v := e.views[0]
	if len(v.loaded) != 0 || v.destroyed != 1 {
		t.Fatalf("loaded = %v, destroyed = %d", v.loaded, v.destroyed)
	}
}

func TestTheHandleReportsAViewWhoseProcessIsGone(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	handle, err := h.Open(frame("local", "127.0.0.1", "t"))
	if err != nil || handle.OnGone == nil {
		t.Fatalf("err = %v, OnGone nil = %v", err, handle.OnGone == nil)
	}
	called := 0
	handle.OnGone(func() { called++ })
	e.views[0].gone()
	if called != 1 {
		t.Fatalf("called = %d", called)
	}
}

type ruleJSON struct {
	Trigger struct {
		Filter string `json:"url-filter"`
	} `json:"trigger"`
	Action struct {
		Type string `json:"type"`
	} `json:"action"`
}

func blocked(t *testing.T, rules, url string) bool {
	t.Helper()
	var list []ruleJSON
	if err := json.Unmarshal([]byte(rules), &list); err != nil {
		t.Fatal(err)
	}
	out := false
	for _, r := range list {
		if !regexp.MustCompile("(?i)" + r.Trigger.Filter).MatchString(url) {
			continue
		}
		switch r.Action.Type {
		case "block":
			out = true
		case "ignore-previous-rules":
			out = false
		default:
			t.Fatalf("action %q", r.Action.Type)
		}
	}
	return out
}

func TestTheRulesLetOnlyTheBoardsOriginLoad(t *testing.T) {
	rules := webhost.OriginRules("http://127.0.0.1:4100")
	for _, u := range []string{
		"http://127.0.0.1:4100/f0/api/v1/doc/a.html?raw&frame",
		"http://127.0.0.1:4100/",
		"data:image/png;base64,AAAA",
		"about:blank",
		"webkit-pdfjs-viewer://pdfjs/web/viewer.html",
	} {
		if blocked(t, rules, u) {
			t.Errorf("%s is blocked", u)
		}
	}
	for _, u := range []string{
		"http://127.0.0.1:4101/x",
		"http://127.0.0.1:41000/x",
		"http://127.0.0.1:410/x",
		"http://127.0.0.1/x",
		"http://127.0.0.2:4100/x",
		"http://127x0x0x1:4100/x",
		"http://127.0.0.1:4100@203.0.113.9/x",
		"http://127.0.0.1:4100.example.com/x",
		"http://localhost:4100/x",
		"https://127.0.0.1:4100/x",
		"ws://127.0.0.1:4100/x",
		"wss://127.0.0.1:4100/x",
		"http://203.0.113.9/x",
		"https://example.com/x",
		"http://127.0.0.1:4101/?u=http://127.0.0.1:4100/",
		"http://127.0.0.1:4101/x/http://127.0.0.1:4100/y",
		"http://127.0.0.2:4100/#http://127.0.0.1:4100/",
		"https://example.com/?next=http%3A%2F%2F127.0.0.1%3A4100%2F&u=http://127.0.0.1:4100/",
		"ws://127.0.0.1:4101/?u=http://127.0.0.1:4100/",
	} {
		if !blocked(t, rules, u) {
			t.Errorf("%s is not blocked", u)
		}
	}
}

func TestTheRulesEscapeAnIPv6Origin(t *testing.T) {
	rules := webhost.OriginRules("http://[::1]:4100")
	if blocked(t, rules, "http://[::1]:4100/x") || !blocked(t, rules, "http://[::2]:4100/x") || !blocked(t, rules, "http://[::1]:4101/x") {
		t.Fatal("the IPv6 origin is not matched exactly")
	}
}

func videoFrame(url string) desktop.DocFrame {
	f := frame("local", "127.0.0.1", "t")
	f.Kind, f.URL = "video", url
	return f
}

const hostileName = `a"onerror="x()<b>&amp;javascript:alert(1)'.mp4`

func TestAVideoLoadsOnlyAPageTheWindowWroteAndNeverTheFileAsADocument(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	f := videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/reports/clip.mp4?raw")
	if _, err := h.Open(f); err != nil {
		t.Fatal(err)
	}
	v := e.views[0]
	if len(v.loaded) != 0 {
		t.Fatalf("the file was loaded as a document: %v", v.loaded)
	}
	if len(v.pages) != 1 || len(v.bases) != 1 || v.bases[0] != "http://127.0.0.1:4100/" {
		t.Fatalf("pages = %d, bases = %v", len(v.pages), v.bases)
	}
	if want := []string{"origin", "html"}; len(v.order) != 2 || v.order[0] != want[0] || v.order[1] != want[1] {
		t.Fatalf("order = %v, want the origin set before the page loads", v.order)
	}
	want := `<video controls preload="metadata" src="` + f.URL + `"></video>`
	if !strings.HasSuffix(v.pages[0], want) {
		t.Fatalf("page = %q, want it to end with %q", v.pages[0], want)
	}
	if strings.Contains(v.pages[0], "autoplay") || strings.Contains(v.pages[0], "<script") {
		t.Fatalf("page has autoplay or a script: %q", v.pages[0])
	}
}

func TestAVideoPageAllowsOnlyMediaFromItsOwnOriginUnderACSP(t *testing.T) {
	page := webhost.VideoPage("http://127.0.0.1:4100/f0/api/v1/doc/a.mp4?raw")
	m := regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]*)">`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no Content-Security-Policy meta: %q", page)
	}
	if m[1] != "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'" {
		t.Fatalf("policy = %q", m[1])
	}
	if strings.Index(page, "Content-Security-Policy") > strings.Index(page, "<video") || strings.Index(page, "Content-Security-Policy") > strings.Index(page, "<style") {
		t.Fatal("the policy comes after content it must cover")
	}
	matte := kit.ColorsFor(true).Screen
	if want := fmt.Sprintf("background:#%02x%02x%02x", matte.R, matte.G, matte.B); !strings.Contains(page, want) || strings.Count(page, "#") != 1 {
		t.Fatalf("the page must use the kit's Screen colour (%s) and no other: %q", want, page)
	}
	if n := strings.Count(page, "<"); n != 6 {
		t.Fatalf("tag openers = %d, want doctype, meta, style and video only: %q", n, page)
	}
}

func TestAVideoNameWithMarkupStaysInsideTheSrcAttribute(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	f := videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/" + hostileName + "?raw")
	if _, err := h.Open(f); err != nil {
		t.Fatal(err)
	}
	page := e.views[0].pages[0]
	i := strings.Index(page, `<video controls preload="metadata" src="`)
	if i < 0 {
		t.Fatalf("no video element: %q", page)
	}
	rest := page[i+len(`<video controls preload="metadata" src="`):]
	end := strings.Index(rest, `"`)
	if end < 0 || rest[end:] != `"></video>` {
		t.Fatalf("the attribute does not end where the page does: %q", rest)
	}
	if got := html.UnescapeString(rest[:end]); got != f.URL {
		t.Fatalf("src = %q, want %q", got, f.URL)
	}
	if strings.ContainsAny(rest[:end], `"<>'`) || strings.Contains(rest[:end], "&amp;amp;amp;") {
		t.Fatalf("src is not escaped: %q", rest[:end])
	}
	if strings.Count(page, "<") != 6 {
		t.Fatalf("the name added a tag: %q", page)
	}
}

func TestAVideoWithAJavascriptNameIsStillOnlyTheHTTPAddress(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	if _, err := h.Open(videoFrame("javascript:alert(1)")); err == nil {
		t.Fatal("opened a javascript: address")
	}
	if len(e.views) != 0 {
		t.Fatal("a view was made")
	}
	if _, err := h.Open(videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/javascript:alert(1).mp4?raw")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.views[0].pages[0], `src="http://127.0.0.1:4100/f0/`) {
		t.Fatalf("page = %q", e.views[0].pages[0])
	}
}

func TestAVideoViewNavigatesNowhereDownloadsNothingAndOpensNoWindow(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	f := videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/reports/clip.mp4?raw")
	if _, err := h.Open(f); err != nil {
		t.Fatal(err)
	}
	v := e.views[0]
	for _, to := range []string{f.URL, "http://127.0.0.1:4100/", "http://127.0.0.1:4100/f0/api/v1/status", "https://example.com/", "about:blank", "javascript:alert(1)", ""} {
		if v.navigate(to) {
			t.Errorf("navigation to %q is allowed", to)
		}
	}
	if v.download(f.URL) || v.download("http://127.0.0.1:4100/x") {
		t.Error("a download is allowed")
	}
	if v.window(f.URL) || v.window("https://example.com/") {
		t.Error("a new window is allowed")
	}
	if v.permit("http://127.0.0.1:4100") || v.permit("") {
		t.Error("a permission is granted")
	}
}

func TestAVideoViewIsConfinedBeforeThePageLoadsAndNeverLoadsWhenItCannotBe(t *testing.T) {
	e := &fakeEngine{originErr: errors.New("content rules are not available")}
	h := &webhost.Host{Engine: e}
	handle, err := h.Open(videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/a.mp4?raw"))
	if !errors.Is(err, desktop.ErrWebUnconfined) {
		t.Fatalf("err = %v", err)
	}
	v := e.views[0]
	if handle.View != nil || handle.Close != nil || len(v.pages) != 0 || len(v.loaded) != 0 || v.destroyed != 1 {
		t.Fatalf("handle = %+v, pages = %d, loaded = %d, destroyed = %d", handle, len(v.pages), len(v.loaded), v.destroyed)
	}
}

func TestAVideoReportsALoadFailureAndAGoneProcessThroughOneHook(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	handle, err := h.Open(videoFrame("http://127.0.0.1:4100/f0/api/v1/doc/a.mp4?raw"))
	if err != nil || handle.OnGone == nil {
		t.Fatalf("err = %v", err)
	}
	called := 0
	handle.OnGone(func() { called++ })
	v := e.views[0]
	if v.gone == nil || v.failed == nil {
		t.Fatal("a signal is not wired")
	}
	v.gone()
	v.failed()
	if called != 2 {
		t.Fatalf("called = %d, want 2", called)
	}
}

func TestADocumentViewDoesNotWatchLoadFailuresAndKeepsItsOwnPolicy(t *testing.T) {
	e := &fakeEngine{}
	h := &webhost.Host{Engine: e}
	handle, err := h.Open(frame("local", "127.0.0.1", "t"))
	if err != nil {
		t.Fatal(err)
	}
	handle.OnGone(func() {})
	v := e.views[0]
	if v.failed != nil || len(v.pages) != 0 {
		t.Fatalf("a document view got the video path: failed set = %v, pages = %d", v.failed != nil, len(v.pages))
	}
}
