package desktop_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type inboxBoard struct {
	mu      sync.Mutex
	csrf    []string
	ctype   string
	field   string
	label   string
	bytes   []byte
	message string
	files   map[string][]byte
	refuse  int
}

func inboxServer(t *testing.T) (*httptest.Server, *inboxBoard) {
	t.Helper()
	b := &inboxBoard{files: map[string][]byte{"20261101-050000-1-a.png": []byte("png-bytes")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/"+fleetID)
		switch {
		case rest == "/proof":
			proof(w, r)
			return
		case rest == "/api/v1/session":
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"x","data":{"csrf":"`+testToken+`"}}`)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && rest == "/api/v1/inbox":
			b.csrf = append(b.csrf, r.Header.Get("X-Hand-CSRF"))
			b.ctype = r.Header.Get("Content-Type")
			if b.refuse != 0 {
				w.WriteHeader(b.refuse)
				io.WriteString(w, `{"error":{"status":413,"code":"too_large","message":"a file can be at most 10 MiB"}}`)
				return
			}
			f, h, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			b.label, b.bytes = h.Filename, nil
			b.bytes, _ = io.ReadAll(f)
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"x","data":{"name":"20261101-050000-1-shot.png","label":"shot.png","kind":"image","size":9}}`)
		case r.Method == http.MethodPost && rest == "/api/v1/message":
			raw, _ := io.ReadAll(r.Body)
			b.message = string(raw)
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"x","data":{"accepted":true}}`)
		case strings.HasPrefix(rest, "/api/v1/inbox/"):
			data, ok := b.files[strings.TrimPrefix(rest, "/api/v1/inbox/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":{"status":404,"code":"not_found","message":"not found: file in inbox/"}}`)
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, b
}

func TestHTTPClientUploadsAFileAsMultipartWithTheCSRFHeader(t *testing.T) {
	srv, b := inboxServer(t)
	c := goldenClient(t, srv.URL)
	got, err := c.UploadInbox(context.Background(), fleetID, "shot.png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "20261101-050000-1-shot.png" || got.Label != "shot.png" || got.Kind != "image" || got.Size != 9 {
		t.Fatalf("answer = %+v", got)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !strings.HasPrefix(b.ctype, "multipart/form-data; boundary=") || b.label != "shot.png" || string(b.bytes) != "png-bytes" {
		t.Fatalf("the board saw %q, label %q, bytes %q", b.ctype, b.label, b.bytes)
	}
	if len(b.csrf) != 1 || b.csrf[0] != testToken {
		t.Fatalf("CSRF headers = %v", b.csrf)
	}
}

func TestHTTPClientReportsTheBoardsRefusalOfAnUpload(t *testing.T) {
	srv, b := inboxServer(t)
	b.refuse = http.StatusRequestEntityTooLarge
	c := goldenClient(t, srv.URL)
	_, err := c.UploadInbox(context.Background(), fleetID, "big.bin", []byte("x"))
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Status != 413 || api.Message != "a file can be at most 10 MiB" {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPClientChecksAndReadsInboxFiles(t *testing.T) {
	srv, _ := inboxServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()
	if n, err := c.InboxHead(ctx, fleetID, "20261101-050000-1-a.png"); err != nil || n != 9 {
		t.Fatalf("HEAD = %d, %v", n, err)
	}
	var api *desktop.APIError
	if _, err := c.InboxHead(ctx, fleetID, "20261101-050000-2-gone.png"); !errors.As(err, &api) || api.Status != 404 {
		t.Fatalf("HEAD of a missing file = %v", err)
	}
	b, err := c.InboxBytes(ctx, fleetID, "20261101-050000-1-a.png")
	if err != nil || string(b) != "png-bytes" {
		t.Fatalf("GET = %q, %v", b, err)
	}
	if _, err := c.InboxBytes(ctx, fleetID, "20261101-050000-2-gone.png"); !errors.As(err, &api) || api.Status != 404 {
		t.Fatalf("GET of a missing file = %v", err)
	}
}

func TestHTTPClientReadsNoMoreThanTheByteCapOfAnInboxFile(t *testing.T) {
	srv, b := inboxServer(t)
	b.files["20261101-050000-3-big.png"] = make([]byte, 10<<20+1)
	c := goldenClient(t, srv.URL)
	if got, err := c.InboxBytes(context.Background(), fleetID, "20261101-050000-3-big.png"); err == nil || got != nil {
		t.Fatalf("a file over 10 MiB read as %d bytes, %v", len(got), err)
	}
}

func TestHTTPClientSendsAttachmentsOnlyWhenThereAreSome(t *testing.T) {
	srv, b := inboxServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()
	if _, err := c.SendMessage(ctx, fleetID, "look", []string{"20261101-050000-1-a.png", "20261101-050000-2-b.log"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	got := b.message
	b.mu.Unlock()
	if got != `{"text":"look","attachments":["20261101-050000-1-a.png","20261101-050000-2-b.log"]}` {
		t.Fatalf("body = %s", got)
	}
	if _, err := c.SendMessage(ctx, fleetID, "plain", nil); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	got = b.message
	b.mu.Unlock()
	if got != `{"text":"plain"}` {
		t.Fatalf("a text-only message sent %s", got)
	}
	if _, err := c.SendMessage(ctx, fleetID, "", []string{"20261101-050000-1-a.png"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	got = b.message
	b.mu.Unlock()
	if got != `{"text":"","attachments":["20261101-050000-1-a.png"]}` {
		t.Fatalf("an attachments-only message sent %s", got)
	}
}
