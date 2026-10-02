package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type server struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	paths []string
}

func (s *server) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.paths)
}

func (s *server) set(name string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = body
}

func release(t *testing.T, files map[string][]byte) *server {
	t.Helper()
	s := &server{files: files}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, req.URL.Path)
		body, ok := s.files[filepath.Base(req.URL.Path)]
		s.mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, req)
		case body == nil:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func tarball(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func handScript(version, channel, commit, schema, luvus string) string {
	lines := "version: " + version + "\\nchannel: " + channel + "\\ncommit: " + commit + "\\n"
	if schema != "" {
		lines += "schema: " + schema + "\\n"
	}
	lines += "luvus: " + luvus + "\\n"
	return "#!/bin/sh\nif [ \"$1\" = version ]; then printf '" + lines + "'; exit 0; fi\nif [ -n \"$HAND_CALLS\" ]; then echo \"hand $* @ $(pwd)\" >> \"$HAND_CALLS\"; fi\nexit ${HAND_EXIT:-0}\n"
}

func fakeHand(t *testing.T, version, channel, commit, schema, luvus string) map[string][]byte {
	t.Helper()
	archive := tarball(t, "hand", handScript(version, channel, commit, schema, luvus))
	return map[string][]byte{
		"hand-linux-amd64.tar.gz": archive,
		"checksums.txt":           []byte(sum(archive) + "  hand-linux-amd64.tar.gz\n" + sum(archive) + "  hand-linux-arm64.tar.gz\n"),
	}
}

func fakeLuvus(t *testing.T, version string) map[string][]byte {
	t.Helper()
	name := "luvus-v" + version + "-x86_64-unknown-linux-musl"
	archive := tarball(t, "luvus", "#!/bin/sh\necho 'luvus "+version+"'\n")
	return map[string][]byte{
		name + ".tar.gz": archive,
		name + ".sha256": []byte(sum(archive) + "  " + name + ".tar.gz\n"),
	}
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists (%v)", path, err)
	}
}

func TestFetchHandReadsTheBuild(t *testing.T) {
	srv := release(t, fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "0.14.4"))
	for channel, want := range map[string]string{"stable": "/latest/download/hand-linux-amd64.tar.gz", "edge": "/download/edge/hand-linux-amd64.tar.gz"} {
		dir := t.TempDir()
		b, err := FetchHand(context.Background(), srv.URL, channel, "amd64", dir, os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		if b != (Build{Path: filepath.Join(dir, "hand"), Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 7, Luvus: "0.14.4"}) {
			t.Fatalf("%s build = %+v", channel, b)
		}
		if !slices.Contains(srv.asked(), want) {
			t.Fatalf("%s asked %q", channel, srv.asked())
		}
	}
}

func TestFetchHandRefusesABadChecksum(t *testing.T) {
	for name, line := range map[string]string{"corrupt": strings.Repeat("0", 64) + "  hand-linux-amd64.tar.gz\n", "missing": strings.Repeat("0", 64) + "  other.tar.gz\n"} {
		files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "0.14.4")
		files["checksums.txt"] = []byte(line)
		dir := t.TempDir()
		if _, err := FetchHand(context.Background(), release(t, files).URL, "edge", "amd64", dir, os.Environ()); err == nil || !strings.Contains(err.Error(), "checksum mismatch for hand-linux-amd64.tar.gz") {
			t.Fatalf("%s: err = %v", name, err)
		}
		absent(t, filepath.Join(dir, "hand"))
	}
}

func TestFetchHandRefusesAFailedDownload(t *testing.T) {
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "0.14.4")
	files["hand-linux-amd64.tar.gz"] = nil
	dir := t.TempDir()
	if _, err := FetchHand(context.Background(), release(t, files).URL, "edge", "amd64", dir, os.Environ()); err == nil || !strings.Contains(err.Error(), ": 500") {
		t.Fatalf("err = %v", err)
	}
	absent(t, filepath.Join(dir, "hand"))
}

func TestFetchLuvusChecksItsSha256(t *testing.T) {
	srv := release(t, fakeLuvus(t, "0.14.4"))
	dir := t.TempDir()
	bin, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "amd64", dir)
	if err != nil || bin != filepath.Join(dir, "luvus") {
		t.Fatalf("FetchLuvus = %q, %v", bin, err)
	}
	if !slices.Contains(srv.asked(), "/download/v0.14.4/luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("asked %q", srv.asked())
	}
	srv.set("luvus-v0.14.4-x86_64-unknown-linux-musl.sha256", []byte(strings.Repeat("0", 64)+"  luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz\n"))
	other := t.TempDir()
	if _, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "amd64", other); err == nil || !strings.Contains(err.Error(), "checksum mismatch for luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("wrong sha256: err = %v", err)
	}
	absent(t, filepath.Join(other, "luvus"))
	if _, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "riscv64", t.TempDir()); err == nil || err.Error() != "update: no Luvus build for riscv64" {
		t.Fatalf("riscv64: err = %v", err)
	}
}

func TestNewerComparesNumbers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.14.10", "0.14.9", true}, {"0.14.4", "", true}, {"0.14.4", "0.14.4", false}, {"0.14.3", "0.15.0", false}, {"x", "0.1.0", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Fatalf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
