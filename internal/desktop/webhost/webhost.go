package webhost

import (
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

type Session interface {
	SetCookie(name, value, domain string) error
}

type View interface {
	Native() ui.NativeWebView
	Load(url string)
	WillNavigate(allow func(url string) bool)
	WillDownload(allow func(url string) bool)
	WillOpenWindow(allow func(url string) bool)
	WillUsePermission(allow func(origin string) bool)
	OnlyOrigin(origin string) error
	OnGone(fn func())
	Destroy()
}

type Engine interface {
	NewSession() (Session, error)
	NewView(Session) (View, error)
}

type Host struct {
	Engine Engine

	mu       sync.Mutex
	sessions map[string]Session
}

func (h *Host) session(host string) (Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[host]; ok {
		return s, nil
	}
	s, err := h.Engine.NewSession()
	if err != nil {
		return nil, err
	}
	if h.sessions == nil {
		h.sessions = map[string]Session{}
	}
	h.sessions[host] = s
	return s, nil
}

func (h *Host) Open(f desktop.DocFrame) (desktop.WebHandle, error) {
	u, err := url.Parse(f.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.Hostname() != f.Cookie.Domain || f.Host == "" {
		return desktop.WebHandle{}, errors.New("the file's address is not one this window loads")
	}
	s, err := h.session(f.Host)
	if err != nil {
		return desktop.WebHandle{}, err
	}
	if err := s.SetCookie(f.Cookie.Name, f.Cookie.Value, f.Cookie.Domain); err != nil {
		return desktop.WebHandle{}, err
	}
	v, err := h.Engine.NewView(s)
	if err != nil {
		return desktop.WebHandle{}, err
	}
	v.WillNavigate(func(to string) bool { return to == f.URL })
	v.WillDownload(func(string) bool { return false })
	v.WillOpenWindow(func(string) bool { return false })
	v.WillUsePermission(func(string) bool { return false })
	if err := v.OnlyOrigin(origin(u)); err != nil {
		v.Destroy()
		return desktop.WebHandle{}, err
	}
	v.Load(f.URL)
	return desktop.WebHandle{View: v.Native(), Close: v.Destroy, OnGone: v.OnGone}, nil
}

func origin(u *url.URL) string {
	host := u.Host
	if u.Port() == "80" {
		host = u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return u.Scheme + "://" + host
}
