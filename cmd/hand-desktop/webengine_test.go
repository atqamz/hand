package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

type recordedPage struct {
	loaded     []string
	navigate   func(*mygo.NavigateEvent)
	download   func(*mygo.DownloadEvent)
	openWindow func(mygo.WindowOpenRequest) *mygo.WindowOptions
	permission func(mygo.PermissionRequest) bool
}

func (p *recordedPage) LoadURL(u string) error { p.loaded = append(p.loaded, u); return nil }

func (p *recordedPage) OnWillNavigate(fn func(*mygo.NavigateEvent)) func() {
	p.navigate = fn
	return func() {}
}

func (p *recordedPage) OnWillDownload(fn func(*mygo.DownloadEvent)) func() {
	p.download = fn
	return func() {}
}

func (p *recordedPage) SetWindowOpenHandler(fn func(mygo.WindowOpenRequest) *mygo.WindowOptions) {
	p.openWindow = fn
}

func (p *recordedPage) SetPermissionHandler(fn func(mygo.PermissionRequest) bool) { p.permission = fn }

type recordedEngine struct {
	opts      []mygo.WebViewOptions
	page      *recordedPage
	destroyed int
	err       error
}

func (r *recordedEngine) engine() mygoEngine {
	return mygoEngine{func(o mygo.WebViewOptions) (webParts, error) {
		if r.err != nil {
			return webParts{}, r.err
		}
		r.opts = append(r.opts, o)
		r.page = &recordedPage{}
		return webParts{page: r.page, destroy: func() { r.destroyed++ }}, nil
	}}
}

func newRecordedView(t *testing.T) (*recordedEngine, mygoView) {
	t.Helper()
	r := &recordedEngine{}
	e := r.engine()
	s, err := e.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.NewView(s)
	if err != nil {
		t.Fatal(err)
	}
	return r, v.(mygoView)
}

func TestTheWebViewIsUntrustedAndInItsOwnEphemeralSession(t *testing.T) {
	t.Parallel()
	r, _ := newRecordedView(t)
	if len(r.opts) != 1 {
		t.Fatalf("want one web view made, got %d", len(r.opts))
	}
	page := r.opts[0].Page
	if !page.Untrusted {
		t.Error("the web view is not Untrusted")
	}
	if page.Session == nil || !page.Session.IsEphemeral() || page.Session == mygo.DefaultSession() {
		t.Errorf("the web view is not in an ephemeral session of its own: %+v", page.Session)
	}
	if !page.PreloadAllFrames {
		t.Error("the preload script does not run in every frame")
	}
	if !strings.Contains(page.PreloadScript, "'print'") || !strings.Contains(page.PreloadScript, "configurable:false") {
		t.Errorf("the preload script does not pin window.print: %q", page.PreloadScript)
	}
}

func TestEverySessionIsANewEphemeralOne(t *testing.T) {
	t.Parallel()
	e := (&recordedEngine{}).engine()
	a, _ := e.NewSession()
	b, _ := e.NewSession()
	sa, sb := a.(mygoSession).s, b.(mygoSession).s
	if sa == sb || !sa.IsEphemeral() || !sb.IsEphemeral() || sa == mygo.DefaultSession() {
		t.Errorf("sessions are shared or not ephemeral: %v %v", sa, sb)
	}
}

func TestTheSessionCookieIsHostOnlyHTTPOnlyAndForTheWholeSite(t *testing.T) {
	t.Parallel()
	var got []mygo.Cookie
	s := mygoSession{set: func(c mygo.Cookie) error { got = append(got, c); return nil }}
	if err := s.SetCookie("hand_phone_f1", "tok", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	want := mygo.Cookie{Name: "hand_phone_f1", Value: "tok", Domain: "127.0.0.1", Path: "/", HTTPOnly: true}
	if len(got) != 1 || got[0] != want {
		t.Errorf("cookie set %+v, want %+v", got, want)
	}
	boom := errors.New("no store")
	s.set = func(mygo.Cookie) error { return boom }
	if err := s.SetCookie("a", "b", "c"); !errors.Is(err, boom) {
		t.Errorf("a failed cookie write is not returned: %v", err)
	}
}

func TestTheWebViewDeniesNavigationOutsideWhatIsAllowed(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	v.WillNavigate(func(u string) bool { return u == "http://127.0.0.1:1/doc?raw" })
	for _, c := range []struct {
		url     string
		blocked bool
	}{
		{"http://127.0.0.1:1/doc?raw", false},
		{"http://127.0.0.1:1/other", true},
		{"https://example.com/", true},
		{"file:///etc/passwd", true},
	} {
		e := &mygo.NavigateEvent{URL: c.url}
		r.page.navigate(e)
		if e.DefaultPrevented() != c.blocked {
			t.Errorf("navigation to %q prevented = %v, want %v", c.url, e.DefaultPrevented(), c.blocked)
		}
	}
}

func TestTheWebViewDeniesADownloadThatIsNotAllowed(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	v.WillDownload(func(string) bool { return false })
	e := &mygo.DownloadEvent{URL: "http://127.0.0.1:1/doc?raw"}
	r.page.download(e)
	if !e.DefaultPrevented() {
		t.Error("a download was not prevented")
	}
	v.WillDownload(func(string) bool { return true })
	e = &mygo.DownloadEvent{URL: "http://127.0.0.1:1/doc?raw"}
	r.page.download(e)
	if e.DefaultPrevented() {
		t.Error("a download the policy allows was prevented")
	}
}

func TestTheWebViewNeverOpensAWindow(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	v.WillOpenWindow(func(string) bool { return true })
	for _, u := range []string{"http://127.0.0.1:1/doc?raw", "https://example.com/", ""} {
		if w := r.page.openWindow(mygo.WindowOpenRequest{URL: u, UserInitiated: true}); w != nil {
			t.Errorf("window.open(%q) was allowed: %+v", u, w)
		}
	}
}

func TestTheWebViewDeniesPermissionsThePolicyDenies(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	var asked []string
	v.WillUsePermission(func(origin string) bool { asked = append(asked, origin); return false })
	if r.page.permission(mygo.PermissionRequest{Origin: "http://127.0.0.1:1"}) {
		t.Error("a permission the policy denies was granted")
	}
	if len(asked) != 1 || asked[0] != "http://127.0.0.1:1" {
		t.Errorf("the policy was asked about %v, want the request's origin", asked)
	}
	v.WillUsePermission(func(string) bool { return true })
	if !r.page.permission(mygo.PermissionRequest{Origin: "o"}) {
		t.Error("a permission the policy allows was denied")
	}
}

func TestTheWebViewLoadsTheAddressAndDestroysItself(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	v.Load("http://127.0.0.1:1/doc?raw")
	if len(r.page.loaded) != 1 || r.page.loaded[0] != "http://127.0.0.1:1/doc?raw" {
		t.Errorf("loaded %v", r.page.loaded)
	}
	v.Destroy()
	if r.destroyed != 1 {
		t.Errorf("destroyed %d times, want once", r.destroyed)
	}
}

func TestAWebViewThatCannotBeMadeIsAnError(t *testing.T) {
	t.Parallel()
	r := &recordedEngine{err: errors.New("no web engine")}
	e := r.engine()
	s, _ := e.NewSession()
	if v, err := e.NewView(s); err == nil || v != nil {
		t.Errorf("NewView = %v, %v; want an error and no view", v, err)
	}
}
