package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	rules      [][]*mygo.ContentRules
	gone       func(string)
	menu       func(*mygo.ContextMenuEvent)
}

func (p *recordedPage) OnContextMenu(fn func(*mygo.ContextMenuEvent)) func() {
	p.menu = fn
	return func() {}
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

func (p *recordedPage) SetContentRules(r ...*mygo.ContentRules) { p.rules = append(p.rules, r) }

func (p *recordedPage) OnRenderProcessGone(fn func(string)) func() {
	p.gone = fn
	return func() {}
}

type recordedEngine struct {
	opts      []mygo.WebViewOptions
	page      *recordedPage
	destroyed int
	err       error
	compiled  []string
	rules     *ruleCache
}

func (r *recordedEngine) engine() mygoEngine {
	r.rules = &ruleCache{compile: func(id, rules string) (*mygo.ContentRules, error) {
		r.compiled = append(r.compiled, id+" "+rules)
		return &mygo.ContentRules{}, nil
	}}
	return mygoEngine{func(o mygo.WebViewOptions) (webParts, error) {
		if r.err != nil {
			return webParts{}, r.err
		}
		r.opts = append(r.opts, o)
		r.page = &recordedPage{}
		return webParts{page: r.page, destroy: func() { r.destroyed++ }, rules: r.rules}, nil
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
	if !strings.Contains(page.PreloadScript, blockLinkHover) {
		t.Errorf("the preload script does not cancel link clicks: %q", page.PreloadScript)
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

func TestTheWebViewShowsNoContextMenu(t *testing.T) {
	t.Parallel()
	r, _ := newRecordedView(t)
	if r.page.menu == nil {
		t.Fatal("no context menu listener is set")
	}
	for _, e := range []*mygo.ContextMenuEvent{
		{},
		{LinkURL: "http://127.0.0.1:1/secret"},
		{ImageURL: "http://127.0.0.1:1/i.png"},
		{SelectionText: "x", Editable: true},
	} {
		r.page.menu(e)
		if !e.DefaultPrevented() {
			t.Errorf("the context menu for %+v was not prevented", *e)
		}
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

func TestTheWebViewIsConfinedToTheBoardsOrigin(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	if err := v.OnlyOrigin("http://127.0.0.1:41001"); err != nil {
		t.Fatal(err)
	}
	if len(r.page.rules) != 1 || len(r.page.rules[0]) != 1 {
		t.Fatalf("content rules set %v, want one list", r.page.rules)
	}
	if len(r.compiled) != 1 || !strings.HasPrefix(r.compiled[0], "hand-board-origin ") || !strings.Contains(r.compiled[0], `127\\.0\\.0\\.1:41001`) {
		t.Errorf("compiled %q, want the Board origin's list under its fixed id", r.compiled)
	}
}

func TestAnOriginsRulesAreCompiledOnceAndNotSharedAcrossOrigins(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	for _, o := range []string{"http://127.0.0.1:41002", "http://127.0.0.1:41002", "http://127.0.0.1:41003"} {
		if err := v.OnlyOrigin(o); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.compiled) != 2 || r.compiled[0] == r.compiled[1] {
		t.Errorf("compiled %q, want one list per origin", r.compiled)
	}
	if len(r.page.rules) != 3 {
		t.Errorf("the rules were set %d times, want once per view call", len(r.page.rules))
	}
}

func TestAFailedRuleCompileLeavesTheViewUnconfinedAndSaysSo(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	boom := errors.New("no content blocker")
	r.rules.compile = func(string, string) (*mygo.ContentRules, error) { return nil, boom }
	if err := v.OnlyOrigin("http://127.0.0.1:41004"); !errors.Is(err, boom) {
		t.Errorf("OnlyOrigin = %v, want the compile error", err)
	}
	if len(r.page.rules) != 0 {
		t.Errorf("rules were set after a failed compile: %v", r.page.rules)
	}
}

func TestTheWebViewReportsAGoneRenderProcess(t *testing.T) {
	t.Parallel()
	r, v := newRecordedView(t)
	var n int
	v.OnGone(func() { n++ })
	r.page.gone("crashed")
	if n != 1 {
		t.Errorf("OnGone fired %d times, want once", n)
	}
}

const printProbe = `
const calls = [];
const window = { print: function () { calls.push("native"); } };
%s
const out = [];
window.print();
out.push(["call", calls.length === 0]);
window.print = function () { calls.push("page"); };
window.print();
out.push(["assignment", calls.length === 0]);
out.push(["returns", window.print() === undefined]);
const d = Object.getOwnPropertyDescriptor(window, "print");
out.push(["accessor", typeof d.get === "function" && typeof d.set === "function" && d.configurable === false]);
let threw = false;
try { Object.defineProperty(window, "print", { value: function () { calls.push("redefined"); } }); } catch (e) { threw = true; }
window.print();
out.push(["redefine", threw && calls.length === 0]);
out.push(["getter", d.get() !== undefined && d.get()() === undefined && calls.length === 0]);
d.set(function () { calls.push("setter"); });
window.print();
out.push(["setter", calls.length === 0]);
console.log(JSON.stringify(out));
`

func TestThePreloadMakesPrintANoOpThatThePageCannotRestore(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("node is needed to run the preload script: %v", err)
		}
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(strings.Replace(printProbe, "%s", blockPDFPrint, 1))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := `[["call",true],["assignment",true],["returns",true],["accessor",true],["redefine",true],["getter",true],["setter",true]]`
	if strings.TrimSpace(string(out)) != want {
		t.Errorf("print probe = %s, want %s", strings.TrimSpace(string(out)), want)
	}
}

const hoverProbe = `
const out = [];
for (const protocol of ["http:", "https:", "webkit-pdfjs-viewer:"]) {
  const styles = [];
  const root = { firstChild: "first", insertBefore: (s, ref) => styles.push([s.textContent, ref]) };
  let seen;
  globalThis.location = { protocol: protocol };
  globalThis.document = { documentElement: root, createElement: () => ({ textContent: "" }) };
  globalThis.MutationObserver = class { constructor(fn) { this.fn = fn; } observe(n, o) { seen = [n === document, o]; globalThis.obs = this; } };
  %s
  const sel = styles[0][0].replace(/\{pointer-events:none!important\}$/, "");
  const link = (matches) => {
    const props = {};
    return { nodeType: 1, props, matches: (s) => matches && s === sel, style: {
      getPropertyValue: (n) => props[n] ? props[n][0] : "", getPropertyPriority: (n) => props[n] ? props[n][1] : "",
      setProperty: (n, v, p) => { props[n] = [v, p]; } } };
  };
  const a = link(true), plain = link(false), later = link(true);
  later.style.setProperty("pointer-events", "auto", "important");
  const nested = link(true);
  const box = { nodeType: 1, matches: () => false, style: plain.style, querySelectorAll: (s) => s === sel ? [nested] : [] };
  obs.fn([{ type: "childList", addedNodes: [a, plain, box, { nodeType: 3 }] }, { type: "attributes", target: later }], obs);
  out.push([protocol, styles.length, styles[0][1], sel, seen[0], seen[1].subtree, seen[1].attributeFilter.join(),
    a.props["pointer-events"], plain.props["pointer-events"], nested.props["pointer-events"], later.props["pointer-events"]]);
}
globalThis.location = { protocol: "http:" };
const styles = [];
const late = { firstChild: null, insertBefore: (s) => styles.push(s.textContent) };
globalThis.document = { documentElement: null, createElement: () => ({ textContent: "" }) };
globalThis.MutationObserver = class { constructor(fn) { this.fn = fn; } observe() { globalThis.obs = this; } };
%s
const before = styles.length;
document.documentElement = late;
obs.fn([], obs);
obs.fn([], obs);
out.push(["late", before, styles.length]);
console.log(JSON.stringify(out));
`

func TestThePreloadTurnsLinksIntoSurfacesThatTakeNoPointer(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("node is needed to run the preload script: %v", err)
		}
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "-")
	cmd.Stdin = strings.NewReader(strings.Replace(strings.Replace(hoverProbe, "%s", blockLinkHover, 1), "%s", blockLinkHover, 1))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	none := `["none","important"]`
	web := `a[*|href],area[*|href]`
	pdf := `a[*|href]:not([href^=\"#\"]),area[*|href]:not([href^=\"#\"])`
	row := func(protocol, sel string) string {
		return fmt.Sprintf(`["%s",1,"first","%s",true,true,"style,href",%s,null,%s,%s]`, protocol, sel, none, none, none)
	}
	want := "[" + row("http:", web) + "," + row("https:", web) + "," + row("webkit-pdfjs-viewer:", pdf) + `,["late",0,1]]`
	if strings.TrimSpace(string(out)) != want {
		t.Errorf("hover probe = %s\nwant %s", strings.TrimSpace(string(out)), want)
	}
}

const boardPolicy = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"

func TestTheProbesPolicyIsTheBoardsPolicyForAnHTMLFile(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "board", "preview.go"))
	if err != nil {
		t.Fatal(err)
	}
	sandbox := regexp.MustCompile(`sandbox\s*=\s*"([^"]+)"`).FindSubmatch(src)
	html := regexp.MustCompile(`isHTML\(name\):\s*ctype, policy = "[^"]+", sandbox\+"([^"]+)"`).FindSubmatch(src)
	if sandbox == nil || html == nil {
		t.Fatal("the Board's HTML policy is not where this test reads it: internal/board/preview.go")
	}
	if got := string(sandbox[1]) + string(html[1]); got != boardPolicy {
		t.Errorf("the Board serves an HTML file with %q, the probe with %q", got, boardPolicy)
	}
}
