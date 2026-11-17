package desktop_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func docServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var uris []string
	envelope := func(w http.ResponseWriter, data string) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintf(w, `{"version":1,"fleet":{"id":%q,"name":"golden"},"cursor":"c1","generated_at":"2026-11-01T05:00:00Z","data":%s}`, fleetID, data)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/" + fleetID
		if r.URL.Path == prefix+"/proof" {
			mac := hmac.New(sha256.New, []byte(testToken))
			mac.Write([]byte(r.URL.Query().Get("nonce")))
			io.WriteString(w, hex.EncodeToString(mac.Sum(nil)))
			return
		}
		uris = append(uris, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if c, err := r.Cookie("hand_board_" + fleetID); err != nil || c.Value != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"no session"}}`)
			return
		}
		doc, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix+"/api/v1/doc/")
		switch {
		case !ok:
			http.NotFound(w, r)
		case doc == "backups/x":
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"status":403,"code":"forbidden","message":"the Board does not show Hand's own files"}}`)
		case doc == "big.png" && r.URL.RawQuery == "raw":
			w.Write(make([]byte, 10<<20+1))
		case r.URL.RawQuery == "raw":
			io.WriteString(w, "raw bytes of "+doc)
		case strings.HasPrefix(r.URL.RawQuery, "from="):
			envelope(w, `{"path":"a.txt","name":"a.txt","from":4,"lines":["four","five"],"next":6,"cut":[{"line":5,"bytes":300000}]}`)
		case doc == "reports":
			envelope(w, `{"path":"reports","name":"reports","kind":"folder","at":"2026-11-01T04:40:00Z","entries":[{"name":"empty","path":"reports/empty","kind":"file","label":"FILE","dir":false,"image":false,"size":0},{"name":"d","path":"reports/d","kind":"folder","label":"DIR","dir":true,"image":false}],"page":2,"page_size":500,"total":501,"pages":2}`)
		default:
			envelope(w, `{"path":"reports/my notes#1.md","name":"my notes#1.md","kind":"markdown","text":true,"size":9,"prev":{"name":"a.md","path":"reports/a.md"},"folder_page":2}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &uris
}

func TestHTTPClientReadsDocuments(t *testing.T) {
	srv, uris := docServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()

	d, err := c.Doc(ctx, fleetID, "reports/my notes#1.md", 1)
	if err != nil || d.Kind != "markdown" || d.Prev == nil || d.Prev.Path != "reports/a.md" || d.FolderPage != 2 || d.Next != nil {
		t.Fatalf("file = %+v, %v", d, err)
	}
	f, err := c.Doc(ctx, fleetID, "reports", 2)
	if err != nil || f.Kind != "folder" || f.Page != 2 || f.Pages != 2 || len(f.Entries) != 2 {
		t.Fatalf("folder = %+v, %v", f, err)
	}
	if e := f.Entries[0]; e.Size == nil || *e.Size != 0 || f.Entries[1].Size != nil || !f.Entries[1].Dir {
		t.Fatalf("entries = %+v", f.Entries)
	}
	w, err := c.DocWindow(ctx, fleetID, "a.txt", 4)
	if err != nil || w.From != 4 || len(w.Lines) != 2 || w.Next != 6 || len(w.Cut) != 1 || w.Cut[0].Line != 5 {
		t.Fatalf("window = %+v, %v", w, err)
	}
	b, err := c.DocBytes(ctx, fleetID, "a b.txt")
	if err != nil || string(b) != "raw bytes of a%20b.txt" {
		t.Fatalf("raw = %q, %v", b, err)
	}
	want := []string{
		"/" + fleetID + "/api/v1/doc/reports/my%20notes%231.md?",
		"/" + fleetID + "/api/v1/doc/reports?page=2",
		"/" + fleetID + "/api/v1/doc/a.txt?from=4",
		"/" + fleetID + "/api/v1/doc/a%20b.txt?raw",
	}
	if fmt.Sprint(*uris) != fmt.Sprint(want) {
		t.Fatalf("requests = %q, want %q", *uris, want)
	}
	if desktop.StoresCursor(c, "/doc/reports") {
		t.Fatal("a document read stored a cursor")
	}
}

func TestHTTPClientKeepsTheBoardsWordsAndTheRawLimit(t *testing.T) {
	srv, _ := docServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()
	_, err := c.Doc(ctx, fleetID, "backups/x", 1)
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Status != 403 || api.Code != "forbidden" || api.Message != "the Board does not show Hand's own files" {
		t.Fatalf("refusal = %v", err)
	}
	if _, err := c.DocBytes(ctx, fleetID, "backups/x"); !errors.As(err, &api) || api.Status != 403 {
		t.Fatalf("raw refusal = %v", err)
	}
	if b, err := c.DocBytes(ctx, fleetID, "big.png"); err == nil || b != nil {
		t.Fatalf("a file over the limit read as %d bytes, %v", len(b), err)
	}
}
