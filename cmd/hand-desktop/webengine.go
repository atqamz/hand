package main

import (
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/webhost"
)

// The PDF policy's default-src 'none' blocks the style attributes that size WebKitGTK's viewer frame, so it stays 300x150.
const sizePDFViewer = `(function(){if(document.contentType!=='application/pdf')return;function fix(){var f=document.querySelector('iframe');if(f){f.style.width='100%';f.style.height='100%';f.style.border='0';f.style.display='block'}if(document.body){document.body.style.margin='0';document.body.style.height='100vh'}}fix();new MutationObserver(fix).observe(document,{childList:true,subtree:true});document.addEventListener('DOMContentLoaded',fix)})()`

// MyGo's web view exposes no WebKitGTK print signal, so window.print is a no-op in every frame; pdf.js's own assignment is ignored.
const blockPDFPrint = `(function(){try{Object.defineProperty(window,'print',{get:function(){return function(){}},set:function(){},configurable:false})}catch(e){}})();`

// WebKitGTK opens an idle connection to a link's address when the link is activated (a click, or Return), before any policy runs.
// Links take no pointer events and no Tab stop, by a rule and inline with priority kept by an observer; in the PDF viewer only links that leave the page.
// An image map, a link in a shadow root and a link child that takes pointer events are not covered.
const guardLinks = `(function(){var web=location.protocol==='http:'||location.protocol==='https:';var sel=web?'a[*|href],area[*|href]':'a[*|href]:not([href^="#"]),area[*|href]:not([href^="#"])';var rule=sel+'{pointer-events:none!important}';var added=false;function ensure(){if(added||!document.documentElement)return;added=true;var s=document.createElement('style');s.textContent=rule;document.documentElement.insertBefore(s,document.documentElement.firstChild)}function pin(e){if(e.nodeType!==1||!e.matches(sel))return;var st=e.style;if(st.getPropertyValue('pointer-events')!=='none'||st.getPropertyPriority('pointer-events')!=='important')st.setProperty('pointer-events','none','important');if(e.getAttribute('tabindex')!=='-1')e.setAttribute('tabindex','-1')}function all(n){pin(n);if(n.querySelectorAll){var l=n.querySelectorAll(sel);for(var i=0;i<l.length;i++)pin(l[i])}}new MutationObserver(function(rs){ensure();for(var i=0;i<rs.length;i++){var r=rs[i];if(r.type==='attributes'){pin(r.target)}else{for(var j=0;j<r.addedNodes.length;j++)all(r.addedNodes[j])}}}).observe(document,{childList:true,subtree:true,attributes:true,attributeFilter:['style','href','tabindex']});ensure()})();`

type webPage interface {
	LoadURL(string) error
	LoadHTML(string, string)
	OnWillNavigate(func(*mygo.NavigateEvent)) func()
	OnWillDownload(func(*mygo.DownloadEvent)) func()
	SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions)
	SetPermissionHandler(func(mygo.PermissionRequest) bool)
	SetContentRules(...*mygo.ContentRules)
	OnRenderProcessGone(func(string)) func()
	OnDidFailLoad(func(*mygo.LoadError)) func()
	OnContextMenu(func(*mygo.ContextMenuEvent)) func()
}

type webParts struct {
	native  ui.NativeWebView
	page    webPage
	destroy func()
	rules   *ruleCache
}

type mygoEngine struct {
	makeView func(mygo.WebViewOptions) (webParts, error)
}

type mygoSession struct {
	s   *mygo.Session
	set func(mygo.Cookie) error
}

type mygoView struct{ p webParts }

func newMygoEngine(win *mygo.Window) mygoEngine {
	rules := &ruleCache{compile: mygo.CompileContentRules}
	return mygoEngine{func(o mygo.WebViewOptions) (webParts, error) {
		v, err := win.NewWebView(o)
		if err != nil {
			return webParts{}, err
		}
		return webParts{v, v.Page(), v.Destroy, rules}, nil
	}}
}

func (e mygoEngine) NewSession() (webhost.Session, error) {
	s := mygo.NewEphemeralSession()
	return mygoSession{s, s.Cookies.Set}, nil
}

func (s mygoSession) SetCookie(name, value, domain string) error {
	return s.set(mygo.Cookie{Name: name, Value: value, Domain: domain, Path: "/", HTTPOnly: true})
}

func (e mygoEngine) NewView(s webhost.Session) (webhost.View, error) {
	p, err := e.makeView(mygo.WebViewOptions{Page: mygo.PageOptions{Session: s.(mygoSession).s, Untrusted: true, PreloadScript: blockPDFPrint + guardLinks + sizePDFViewer, PreloadAllFrames: true}})
	if err != nil {
		return nil, err
	}
	p.page.OnContextMenu(func(e *mygo.ContextMenuEvent) { e.PreventDefault() })
	return mygoView{p}, nil
}

func (v mygoView) Native() ui.NativeWebView { return v.p.native }

func (v mygoView) Load(url string) { v.p.page.LoadURL(url) }

func (v mygoView) LoadHTML(page, base string) { v.p.page.LoadHTML(page, base) }

func (v mygoView) WillNavigate(allow func(string) bool) {
	v.p.page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if !allow(e.URL) {
			e.PreventDefault()
		}
	})
}

func (v mygoView) WillDownload(allow func(string) bool) {
	v.p.page.OnWillDownload(func(e *mygo.DownloadEvent) {
		if !allow(e.URL) {
			e.PreventDefault()
		}
	})
}

func (v mygoView) WillOpenWindow(func(string) bool) {
	v.p.page.SetWindowOpenHandler(func(r mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
}

func (v mygoView) WillUsePermission(allow func(string) bool) {
	v.p.page.SetPermissionHandler(func(r mygo.PermissionRequest) bool { return allow(r.Origin) })
}

func (v mygoView) OnlyOrigin(origin string) error {
	rules, err := v.p.rules.get(origin)
	if err != nil {
		return err
	}
	v.p.page.SetContentRules(rules)
	return nil
}

func (v mygoView) OnGone(fn func()) {
	if fn != nil {
		v.p.page.OnRenderProcessGone(func(string) { fn() })
	}
}

func (v mygoView) OnFailed(fn func()) {
	if fn != nil {
		v.p.page.OnDidFailLoad(func(*mygo.LoadError) { fn() })
	}
}

type ruleCache struct {
	sync.Mutex
	compile func(id, rules string) (*mygo.ContentRules, error)
	origin  string
	rules   *mygo.ContentRules
}

func (c *ruleCache) get(origin string) (*mygo.ContentRules, error) {
	c.Lock()
	if c.rules != nil && c.origin == origin {
		defer c.Unlock()
		return c.rules, nil
	}
	c.Unlock()
	rules, err := c.compile("hand-board-origin", webhost.OriginRules(origin))
	if err != nil {
		return nil, err
	}
	c.Lock()
	c.origin, c.rules = origin, rules
	c.Unlock()
	return rules, nil
}

func (v mygoView) Destroy() { v.p.destroy() }
