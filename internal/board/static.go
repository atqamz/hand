package board

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var staticFiles embed.FS

type staticAsset struct {
	name  string
	body  []byte
	ctype string
}

var (
	assetNames = map[string]string{}
	assetFiles = map[string]staticAsset{}
	assetTypes = map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".svg": "image/svg+xml"}
)

func init() {
	entries, err := fs.ReadDir(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		body, err := staticFiles.ReadFile("static/" + e.Name())
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(body)
		ext := path.Ext(e.Name())
		hashed := strings.TrimSuffix(e.Name(), ext) + "." + hex.EncodeToString(sum[:])[:12] + ext
		assetNames[e.Name()] = hashed
		assetFiles[hashed] = staticAsset{name: hashed, body: body, ctype: assetTypes[ext]}
	}
}

func assetURL(name string) string { return "/static/" + assetNames[name] }

func ServeStatic(w http.ResponseWriter, r *http.Request) {
	a, ok := assetFiles[strings.TrimPrefix(r.URL.Path, "/static/")]
	if !ok || r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.ctype)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(a.body)
}
