package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/webhost"
)

// The PDF policy's default-src 'none' blocks the style attributes that size WebKitGTK's viewer frame, so it stays 300x150.
const sizePDFViewer = `(function(){if(document.contentType!=='application/pdf')return;function fix(){var f=document.querySelector('iframe');if(f){f.style.width='100%';f.style.height='100%';f.style.border='0';f.style.display='block'}if(document.body){document.body.style.margin='0';document.body.style.height='100vh'}}fix();new MutationObserver(fix).observe(document,{childList:true,subtree:true});document.addEventListener('DOMContentLoaded',fix)})()`

type mygoEngine struct{ win *mygo.Window }

type mygoSession struct{ s *mygo.Session }

type mygoView struct{ v *mygo.WebView }

func (e mygoEngine) NewSession() (webhost.Session, error) {
	return mygoSession{mygo.NewEphemeralSession()}, nil
}

func (s mygoSession) SetCookie(name, value, domain string) error {
	return s.s.Cookies.Set(mygo.Cookie{Name: name, Value: value, Domain: domain, Path: "/", HTTPOnly: true})
}

func (e mygoEngine) NewView(s webhost.Session) (webhost.View, error) {
	v, err := e.win.NewWebView(mygo.WebViewOptions{Page: mygo.PageOptions{Session: s.(mygoSession).s, Untrusted: true, PreloadScript: sizePDFViewer}})
	if err != nil {
		return nil, err
	}
	return mygoView{v}, nil
}

func (v mygoView) Native() ui.NativeWebView { return v.v }

func (v mygoView) Load(url string) { v.v.Page().LoadURL(url) }

func (v mygoView) WillNavigate(allow func(string) bool) {
	v.v.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
		if !allow(e.URL) {
			e.PreventDefault()
		}
	})
}

func (v mygoView) WillDownload(allow func(string) bool) {
	v.v.Page().OnWillDownload(func(e *mygo.DownloadEvent) {
		if !allow(e.URL) {
			e.PreventDefault()
		}
	})
}

func (v mygoView) WillOpenWindow(func(string) bool) {
	v.v.Page().SetWindowOpenHandler(func(r mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
}

func (v mygoView) WillUsePermission(allow func(string) bool) {
	v.v.Page().SetPermissionHandler(func(r mygo.PermissionRequest) bool { return allow(r.Origin) })
}

func (v mygoView) Destroy() { v.v.Destroy() }
