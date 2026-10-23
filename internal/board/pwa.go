package board

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
)

type proxied struct {
	http.ResponseWriter
	on bool
}

func (p proxied) Flush() {
	if f, ok := p.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (p proxied) Unwrap() http.ResponseWriter { return p.ResponseWriter }

func installable(r *http.Request) bool { return secure(r) && forwarded(r) && !viaFunnel(r) }

func installed(w http.ResponseWriter) bool {
	for {
		switch v := w.(type) {
		case proxied:
			return v.on
		case interface{ Unwrap() http.ResponseWriter }:
			w = v.Unwrap()
		default:
			return false
		}
	}
}

type icon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

var manifestJSON = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"id": "/", "name": "Hand Board", "short_name": "Hand", "lang": "en",
		"start_url": "/", "scope": "/", "display": "standalone",
		"background_color": "#ffffff", "theme_color": "#ffffff",
		"icons": []icon{
			{assetURL("icon-192.png"), "192x192", "image/png", "any"},
			{assetURL("icon-512.png"), "512x512", "image/png", "any"},
			{assetURL("icon-maskable-512.png"), "512x512", "image/png", "maskable"},
		},
	})
	return buf.Bytes()
})

var offlineHTML = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, "offline.html", map[string]any{}); err != nil {
		panic(err)
	}
	return buf.Bytes()
})

var workerJS = sync.OnceValue(func() []byte {
	sum := sha256.New()
	sum.Write(workerSource)
	sum.Write(offlineHTML())
	return append([]byte(`const BUILD = "`+hex.EncodeToString(sum.Sum(nil))[:12]+"\";\n"), workerSource...)
})

func serveFile(w http.ResponseWriter, ctype string, body []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

func manifest(w http.ResponseWriter, _ *http.Request) {
	serveFile(w, "application/manifest+json", manifestJSON())
}

func worker(w http.ResponseWriter, _ *http.Request) {
	serveFile(w, "text/javascript; charset=utf-8", workerJS())
}

func offline(w http.ResponseWriter, _ *http.Request) {
	serveFile(w, "text/html; charset=utf-8", offlineHTML())
}
