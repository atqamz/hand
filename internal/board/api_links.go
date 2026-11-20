package board

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

const (
	linksMax      = 50
	linkValueMax  = 512
	linksQueryMax = 16 << 10
	linksBadMsg   = "links takes 1 to 50 path values, each at most 512 bytes and without NUL, in a query of at most 16 KiB"
)

type APILink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
	Dir  bool   `json:"dir"`
}

type APILinks struct {
	Limit int        `json:"limit"`
	Links []*APILink `json:"links"`
}

func (b *Board) apiLinks(w http.ResponseWriter, r *http.Request) {
	vals, err := url.ParseQuery(r.URL.RawQuery)
	paths := vals["path"]
	bad := err != nil || len(r.URL.RawQuery) > linksQueryMax || len(paths) < 1 || len(paths) > linksMax
	for _, p := range paths {
		bad = bad || len(p) > linkValueMax || strings.Contains(p, "\x00")
	}
	if bad {
		b.apiFail(w, http.StatusBadRequest, "bad_request", linksBadMsg)
		return
	}
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return
	}
	out := APILinks{Limit: linksMax, Links: make([]*APILink, len(paths))}
	for i, p := range paths {
		if rel, href, dir, ok := docLink(b.o.Home, b.o.Base, p); ok {
			out.Links[i] = &APILink{Rel: filepath.ToSlash(rel), Href: href, Dir: dir}
		}
	}
	b.apiOK(w, f, since, out)
}
